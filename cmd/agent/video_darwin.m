#import <Foundation/Foundation.h>
#import <ScreenCaptureKit/ScreenCaptureKit.h>
#import <VideoToolbox/VideoToolbox.h>
#import <CoreMedia/CoreMedia.h>
#import <CoreGraphics/CoreGraphics.h>
#include "video_darwin.h"

extern void goVideoFrame(void *data, int n, int key);

static SCStream *gStream;
static VTCompressionSessionRef gVT;
static CVPixelBufferRef gLast;
static id gOut;
static volatile int gAlive;
static dispatch_queue_t gQueue;

static void setNum(VTCompressionSessionRef s, CFStringRef key, int v) {
	CFNumberRef n = CFNumberCreate(NULL, kCFNumberIntType, &v);
	VTSessionSetProperty(s, key, n);
	CFRelease(n);
}

static void setBitrate(VTCompressionSessionRef s, int bps) {
	setNum(s, kVTCompressionPropertyKey_AverageBitRate, bps);
	// Spitzen auf ca. 1,5x des Mittels pro Sekunde begrenzen (Bytes, Sekunden)
	int64_t bytes = (int64_t)bps / 8 * 3 / 2;
	int64_t secs = 1;
	CFNumberRef a = CFNumberCreate(NULL, kCFNumberSInt64Type, &bytes);
	CFNumberRef b = CFNumberCreate(NULL, kCFNumberSInt64Type, &secs);
	const void *vals[2] = {a, b};
	CFArrayRef arr = CFArrayCreate(NULL, vals, 2, &kCFTypeArrayCallBacks);
	VTSessionSetProperty(s, kVTCompressionPropertyKey_DataRateLimits, arr);
	CFRelease(arr); CFRelease(a); CFRelease(b);
}

static void vtOutput(void *refcon, void *src, OSStatus status, VTEncodeInfoFlags flags, CMSampleBufferRef sb) {
	if (status != noErr || !sb || !CMSampleBufferDataIsReady(sb)) return;
	BOOL key = YES;
	CFArrayRef att = CMSampleBufferGetSampleAttachmentsArray(sb, false);
	if (att && CFArrayGetCount(att) > 0) {
		CFDictionaryRef d = CFArrayGetValueAtIndex(att, 0);
		key = !CFDictionaryContainsKey(d, kCMSampleAttachmentKey_NotSync);
	}
	static const uint8_t sc[4] = {0, 0, 0, 1};
	NSMutableData *out = [NSMutableData dataWithCapacity:64 * 1024];
	if (key) {
		CMFormatDescriptionRef fd = CMSampleBufferGetFormatDescription(sb);
		size_t cnt = 0;
		CMVideoFormatDescriptionGetH264ParameterSetAtIndex(fd, 0, NULL, NULL, &cnt, NULL);
		for (size_t i = 0; i < cnt; i++) {
			const uint8_t *ps; size_t sz;
			if (CMVideoFormatDescriptionGetH264ParameterSetAtIndex(fd, i, &ps, &sz, NULL, NULL) == noErr) {
				[out appendBytes:sc length:4];
				[out appendBytes:ps length:sz];
			}
		}
	}
	CMBlockBufferRef bb = CMSampleBufferGetDataBuffer(sb);
	size_t len = 0; char *ptr = NULL;
	if (!bb || CMBlockBufferGetDataPointer(bb, 0, NULL, &len, &ptr) != kCMBlockBufferNoErr) return;
	size_t off = 0;
	while (off + 4 <= len) { // AVCC (Längenpräfix) -> Annex B (Startcode)
		uint32_t n; memcpy(&n, ptr + off, 4); n = CFSwapInt32BigToHost(n); off += 4;
		if (off + n > len) break;
		[out appendBytes:sc length:4];
		[out appendBytes:ptr + off length:n];
		off += n;
	}
	goVideoFrame((void *)out.bytes, (int)out.length, key ? 1 : 0);
}

static void encode(CVPixelBufferRef pb, BOOL force) {
	if (!gVT || !pb) return;
	CFDictionaryRef props = NULL;
	if (force) {
		const void *k[1] = {kVTEncodeFrameOptionKey_ForceKeyFrame};
		const void *v[1] = {kCFBooleanTrue};
		props = CFDictionaryCreate(NULL, k, v, 1, &kCFTypeDictionaryKeyCallBacks, &kCFTypeDictionaryValueCallBacks);
	}
	VTCompressionSessionEncodeFrame(gVT, pb, CMClockGetTime(CMClockGetHostTimeClock()), kCMTimeInvalid, props, NULL, NULL);
	if (props) CFRelease(props);
}

@interface FWOut : NSObject <SCStreamOutput, SCStreamDelegate>
@end
@implementation FWOut
- (void)stream:(SCStream *)s didOutputSampleBuffer:(CMSampleBufferRef)sb ofType:(SCStreamOutputType)t {
	if (t != SCStreamOutputTypeScreen || !CMSampleBufferIsValid(sb)) return;
	CFArrayRef atts = CMSampleBufferGetSampleAttachmentsArray(sb, false);
	if (atts && CFArrayGetCount(atts) > 0) {
		NSDictionary *d = (__bridge NSDictionary *)CFArrayGetValueAtIndex(atts, 0);
		NSNumber *st = d[SCStreamFrameInfoStatus];
		if (st && st.integerValue != SCFrameStatusComplete) return; // Idle/Blank: keine Änderung
	}
	CVPixelBufferRef pb = CMSampleBufferGetImageBuffer(sb);
	if (!pb) return;
	@synchronized (self) {
		if (gLast) CVPixelBufferRelease(gLast);
		gLast = CVPixelBufferRetain(pb);
	}
	encode(pb, NO);
}
- (void)stream:(SCStream *)s didStopWithError:(NSError *)error {
	gAlive = 0;
}
@end

unsigned int fwDisplayID(int index) {
	CGDirectDisplayID ids[16]; uint32_t n = 0;
	if (CGGetActiveDisplayList(16, ids, &n) != kCGErrorSuccess || index < 0 || index >= (int)n) return 0;
	return ids[index];
}

int fwScreenAccess(void) {
	return CGPreflightScreenCaptureAccess() ? 1 : 0;
}

static void seterr(char *err, int len, NSString *s) {
	if (err && len > 0) strlcpy(err, s.UTF8String, len);
}

int fwVideoStart(unsigned int displayID, int w, int h, int fps, int bitrate, char *err, int errlen) {
	fwVideoStop();
	__block SCShareableContent *content = nil;
	__block NSError *cerr = nil;
	dispatch_semaphore_t sem = dispatch_semaphore_create(0);
	[SCShareableContent getShareableContentWithCompletionHandler:^(SCShareableContent *c, NSError *e) {
		content = c; cerr = e; dispatch_semaphore_signal(sem);
	}];
	if (dispatch_semaphore_wait(sem, dispatch_time(DISPATCH_TIME_NOW, 60 * NSEC_PER_SEC)) != 0) { seterr(err, errlen, @"Zeitüberschreitung bei der Bildschirmfreigabe"); return -1; }
	if (!content) { seterr(err, errlen, cerr ? cerr.localizedDescription : @"keine Bildschirmfreigabe"); return -1; }
	SCDisplay *disp = nil;
	for (SCDisplay *d in content.displays) if (d.displayID == displayID) disp = d;
	if (!disp) { seterr(err, errlen, @"Bildschirm nicht gefunden"); return -1; }

	OSStatus st = VTCompressionSessionCreate(NULL, w, h, kCMVideoCodecType_H264, NULL, NULL, NULL, vtOutput, NULL, &gVT);
	if (st != noErr || !gVT) { seterr(err, errlen, [NSString stringWithFormat:@"VideoToolbox-Fehler %d", (int)st]); gVT = NULL; return -1; }
	VTSessionSetProperty(gVT, kVTCompressionPropertyKey_RealTime, kCFBooleanTrue);
	VTSessionSetProperty(gVT, kVTCompressionPropertyKey_ProfileLevel, kVTProfileLevel_H264_Main_AutoLevel);
	VTSessionSetProperty(gVT, kVTCompressionPropertyKey_AllowFrameReordering, kCFBooleanFalse);
	VTSessionSetProperty(gVT, kVTCompressionPropertyKey_PrioritizeEncodingSpeedOverQuality, kCFBooleanTrue);
	setNum(gVT, kVTCompressionPropertyKey_ExpectedFrameRate, fps);
	setNum(gVT, kVTCompressionPropertyKey_MaxKeyFrameInterval, fps * 8);
	setNum(gVT, kVTCompressionPropertyKey_MaxKeyFrameIntervalDuration, 8);
	setBitrate(gVT, bitrate);
	VTCompressionSessionPrepareToEncodeFrames(gVT);

	SCContentFilter *filter = [[SCContentFilter alloc] initWithDisplay:disp excludingWindows:@[]];
	SCStreamConfiguration *cfg = [[SCStreamConfiguration alloc] init];
	cfg.width = w;
	cfg.height = h;
	cfg.minimumFrameInterval = CMTimeMake(1, fps);
	cfg.pixelFormat = kCVPixelFormatType_420YpCbCr8BiPlanarVideoRange;
	cfg.showsCursor = NO; // der Viewer zeigt den lokalen Zeiger
	cfg.queueDepth = 4;
	gOut = [[FWOut alloc] init];
	gQueue = dispatch_queue_create("remoteMe.capture", DISPATCH_QUEUE_SERIAL);
	gStream = [[SCStream alloc] initWithFilter:filter configuration:cfg delegate:gOut];
	NSError *aerr = nil;
	if (![gStream addStreamOutput:gOut type:SCStreamOutputTypeScreen sampleHandlerQueue:gQueue error:&aerr]) {
		seterr(err, errlen, aerr.localizedDescription); fwVideoStop(); return -1;
	}
	__block NSError *serr = nil;
	[gStream startCaptureWithCompletionHandler:^(NSError *e) { serr = e; dispatch_semaphore_signal(sem); }];
	if (dispatch_semaphore_wait(sem, dispatch_time(DISPATCH_TIME_NOW, 60 * NSEC_PER_SEC)) != 0) { seterr(err, errlen, @"Zeitüberschreitung beim Start der Aufnahme"); fwVideoStop(); return -1; }
	if (serr) { seterr(err, errlen, serr.localizedDescription); fwVideoStop(); return -1; }
	gAlive = 1;
	return 0;
}

void fwVideoKey(void) {
	CVPixelBufferRef pb = NULL;
	@synchronized (gOut) { if (gLast) pb = CVPixelBufferRetain(gLast); }
	if (pb) { encode(pb, YES); CVPixelBufferRelease(pb); }
}

void fwVideoBitrate(int bps) {
	if (gVT) setBitrate(gVT, bps);
}

int fwVideoAlive(void) { return gAlive; }

void fwVideoStop(void) {
	gAlive = 0;
	if (gStream) {
		dispatch_semaphore_t sem = dispatch_semaphore_create(0);
		[gStream stopCaptureWithCompletionHandler:^(NSError *e) { dispatch_semaphore_signal(sem); }];
		dispatch_semaphore_wait(sem, dispatch_time(DISPATCH_TIME_NOW, 5 * NSEC_PER_SEC));
		gStream = nil;
	}
	if (gVT) {
		VTCompressionSessionCompleteFrames(gVT, kCMTimeInvalid);
		VTCompressionSessionInvalidate(gVT);
		CFRelease(gVT);
		gVT = NULL;
	}
	if (gLast) { CVPixelBufferRelease(gLast); gLast = NULL; }
	gOut = nil;
}
