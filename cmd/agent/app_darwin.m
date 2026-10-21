#import <Cocoa/Cocoa.h>
#import <ApplicationServices/ApplicationServices.h>
#include "app_darwin.h"

extern char *goAppStatus(void);
extern void goAppOpenUI(void);
extern void goAppQuit(void);

@interface FWApp : NSObject
@property(strong) NSStatusItem *item;
@property(strong) NSMenuItem *statusLine;
@end

@implementation FWApp
- (void)setup {
	self.item = [[NSStatusBar systemStatusBar] statusItemWithLength:NSVariableStatusItemLength];
	NSImage *img = [NSImage imageWithSystemSymbolName:@"display" accessibilityDescription:@"remoteMe"];
	if (img) { img.template = YES; self.item.button.image = img; }
	else self.item.button.title = @"rM";
	NSMenu *m = [[NSMenu alloc] init];
	self.statusLine = [m addItemWithTitle:@"remoteMe" action:nil keyEquivalent:@""];
	self.statusLine.enabled = NO;
	[m addItem:[NSMenuItem separatorItem]];
	NSMenuItem *a = [m addItemWithTitle:@"Einstellungen öffnen …" action:@selector(openUI:) keyEquivalent:@""];
	a.target = self;
	NSMenuItem *b = [m addItemWithTitle:@"Berechtigungen prüfen …" action:@selector(perms:) keyEquivalent:@""];
	b.target = self;
	[m addItem:[NSMenuItem separatorItem]];
	NSMenuItem *q = [m addItemWithTitle:@"remoteMe beenden" action:@selector(quit:) keyEquivalent:@"q"];
	q.target = self;
	self.item.menu = m;
	[NSTimer scheduledTimerWithTimeInterval:2 target:self selector:@selector(refresh:) userInfo:nil repeats:YES];
	[self refresh:nil];
}
- (void)refresh:(id)t {
	char *s = goAppStatus();
	if (s) { self.statusLine.title = [NSString stringWithUTF8String:s]; free(s); }
}
- (void)openUI:(id)s { goAppOpenUI(); }
- (void)perms:(id)s { fwRequestPermissions(); }
- (void)quit:(id)s { goAppQuit(); }
@end

static FWApp *gApp;

int fwAccessibilityTrusted(void) { return AXIsProcessTrusted() ? 1 : 0; }

// Fragt fehlende Freigaben an; sind sie verweigert, öffnet sich die passende Systemeinstellung.
void fwRequestPermissions(void) {
	dispatch_async(dispatch_get_main_queue(), ^{
		if (!CGPreflightScreenCaptureAccess()) {
			if (!CGRequestScreenCaptureAccess())
				[[NSWorkspace sharedWorkspace] openURL:[NSURL URLWithString:@"x-apple.systempreferences:com.apple.preference.security?Privacy_ScreenCapture"]];
		}
		if (!AXIsProcessTrusted()) {
			NSDictionary *o = @{(__bridge NSString *)kAXTrustedCheckOptionPrompt: @YES};
			AXIsProcessTrustedWithOptions((__bridge CFDictionaryRef)o);
		}
	});
}

void fwRunApp(void) {
	@autoreleasepool {
		NSApplication *app = [NSApplication sharedApplication];
		[app setActivationPolicy:NSApplicationActivationPolicyAccessory];
		gApp = [[FWApp alloc] init];
		[gApp setup];
		fwRequestPermissions();
		[app run];
	}
}

void fwQuitApp(void) {
	dispatch_async(dispatch_get_main_queue(), ^{
		[NSApp stop:nil];
		NSEvent *e = [NSEvent otherEventWithType:NSEventTypeApplicationDefined location:NSZeroPoint modifierFlags:0 timestamp:0 windowNumber:0 context:nil subtype:0 data1:0 data2:0];
		[NSApp postEvent:e atStart:YES];
	});
}
