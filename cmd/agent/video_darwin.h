#ifndef FW_VIDEO_DARWIN_H
#define FW_VIDEO_DARWIN_H

// Nativer Videostrom: ScreenCaptureKit -> VideoToolbox (H.264, Hardware).
unsigned int fwDisplayID(int index);
int fwVideoStart(unsigned int displayID, int w, int h, int fps, int bitrate, char *err, int errlen);
void fwVideoStop(void);
void fwVideoKey(void);
void fwVideoBitrate(int bps);
int fwVideoAlive(void);
int fwScreenAccess(void);

#endif
