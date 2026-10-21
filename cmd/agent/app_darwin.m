#import <Cocoa/Cocoa.h>
#import <ApplicationServices/ApplicationServices.h>
#include "app_darwin.h"

// Von Go bereitgestellt (app_darwin.go)
extern char *goAppStatus(void);
extern char *goWinState(void);
extern char *goWinPair(const char *server, const char *user, const char *name, int autostart);
extern void goWinUnpair(void);
extern void goWinAutostart(int on);
extern void goAppQuit(void);

static NSTextField *label(NSString *t, NSRect f, CGFloat size, BOOL bold, NSColor *c) {
	NSTextField *l = [NSTextField labelWithString:t];
	l.frame = f;
	l.font = bold ? [NSFont boldSystemFontOfSize:size] : [NSFont systemFontOfSize:size];
	if (c) l.textColor = c;
	return l;
}

static NSTextField *wrapLabel(NSString *t, NSRect f, NSColor *c) {
	NSTextField *l = [NSTextField wrappingLabelWithString:t];
	l.frame = f;
	l.font = [NSFont systemFontOfSize:12];
	l.textColor = c ?: NSColor.secondaryLabelColor;
	return l;
}

@interface FWApp : NSObject <NSApplicationDelegate>
@property(strong) NSStatusItem *item;
@property(strong) NSMenuItem *statusLine;
@property(strong) NSWindow *win;
@property(strong) NSView *setupView, *statusView;
@property(strong) NSTextField *fServer, *fUser, *fName, *setupErr;
@property(strong) NSButton *cAuto, *cAuto2, *goBtn, *unpairBtn;
@property(strong) NSTextField *stText, *stCode, *stInfo, *stHint, *stErr;
@property(strong) NSView *dot;
@property(assign) BOOL prefilled, busy;
@end

@implementation FWApp

#define W 440
#define H 430

- (void)buildWindow {
	self.win = [[NSWindow alloc] initWithContentRect:NSMakeRect(0, 0, W, H)
		styleMask:NSWindowStyleMaskTitled | NSWindowStyleMaskClosable | NSWindowStyleMaskMiniaturizable
		backing:NSBackingStoreBuffered defer:NO];
	self.win.title = @"remoteMe";
	self.win.releasedWhenClosed = NO;
	[self.win center];
	NSView *root = self.win.contentView;

	// ---- Einrichtung ----
	NSView *s = [[NSView alloc] initWithFrame:NSMakeRect(0, 0, W, H)];
	[s addSubview:label(@"remoteMe einrichten", NSMakeRect(28, H - 62, W - 56, 26), 20, YES, nil)];
	[s addSubview:wrapLabel(@"Dieser Computer soll von der Ferne aus erreichbar sein. Gib den Benutzer ein, der Zugriff bekommen soll.", NSMakeRect(28, H - 112, W - 56, 44), nil)];
	CGFloat y = H - 142;
	[s addSubview:label(@"Server", NSMakeRect(28, y, W - 56, 16), 12, YES, nil)];
	self.fServer = [[NSTextField alloc] initWithFrame:NSMakeRect(28, y - 28, W - 56, 24)];
	self.fServer.placeholderString = @"https://remoteme.example.com";
	[s addSubview:self.fServer];
	y -= 62;
	[s addSubview:label(@"Benutzer, der zugreifen darf", NSMakeRect(28, y, W - 56, 16), 12, YES, nil)];
	self.fUser = [[NSTextField alloc] initWithFrame:NSMakeRect(28, y - 28, W - 56, 24)];
	self.fUser.placeholderString = @"Benutzername";
	[s addSubview:self.fUser];
	y -= 62;
	[s addSubview:label(@"Name dieses Computers", NSMakeRect(28, y, W - 56, 16), 12, YES, nil)];
	self.fName = [[NSTextField alloc] initWithFrame:NSMakeRect(28, y - 28, W - 56, 24)];
	[s addSubview:self.fName];
	y -= 52;
	self.cAuto = [NSButton checkboxWithTitle:@"Beim Anmelden automatisch starten" target:self action:nil];
	self.cAuto.frame = NSMakeRect(28, y, W - 56, 20);
	self.cAuto.state = NSControlStateValueOn;
	[s addSubview:self.cAuto];
	self.setupErr = wrapLabel(@"", NSMakeRect(28, 62, W - 56, 34), NSColor.systemRedColor);
	[s addSubview:self.setupErr];
	self.goBtn = [NSButton buttonWithTitle:@"Verbinden" target:self action:@selector(doPair:)];
	self.goBtn.frame = NSMakeRect(W - 28 - 120, 20, 120, 32);
	self.goBtn.keyEquivalent = @"\r";
	self.goBtn.bezelStyle = NSBezelStyleRounded;
	[s addSubview:self.goBtn];
	self.setupView = s;

	// ---- Status ----
	NSView *t = [[NSView alloc] initWithFrame:NSMakeRect(0, 0, W, H)];
	[t addSubview:label(@"remoteMe", NSMakeRect(28, H - 62, W - 56, 26), 20, YES, nil)];
	self.dot = [[NSView alloc] initWithFrame:NSMakeRect(28, H - 96, 12, 12)];
	self.dot.wantsLayer = YES;
	self.dot.layer.cornerRadius = 6;
	[t addSubview:self.dot];
	self.stText = label(@"", NSMakeRect(48, H - 100, W - 76, 20), 14, YES, nil);
	[t addSubview:self.stText];
	self.stHint = wrapLabel(@"Gib diesen Code in der Weboberfläche beim neuen Gerät ein, um den Zugriff freizugeben:", NSMakeRect(28, H - 160, W - 56, 44), nil);
	[t addSubview:self.stHint];
	self.stCode = label(@"", NSMakeRect(28, H - 240, W - 56, 64), 44, YES, nil);
	self.stCode.font = [NSFont monospacedSystemFontOfSize:44 weight:NSFontWeightBold];
	self.stCode.alignment = NSTextAlignmentCenter;
	[t addSubview:self.stCode];
	self.stInfo = wrapLabel(@"", NSMakeRect(28, H - 300, W - 56, 44), nil);
	[t addSubview:self.stInfo];
	self.stErr = wrapLabel(@"", NSMakeRect(28, 100, W - 56, 40), NSColor.systemRedColor);
	[t addSubview:self.stErr];
	self.cAuto2 = [NSButton checkboxWithTitle:@"Beim Anmelden automatisch starten" target:self action:@selector(toggleAuto:)];
	self.cAuto2.frame = NSMakeRect(28, 70, W - 56, 20);
	[t addSubview:self.cAuto2];
	self.unpairBtn = [NSButton buttonWithTitle:@"Zugriff widerrufen und Kopplung lösen" target:self action:@selector(doUnpair:)];
	self.unpairBtn.frame = NSMakeRect(28, 20, W - 56, 32);
	self.unpairBtn.bezelStyle = NSBezelStyleRounded;
	[t addSubview:self.unpairBtn];
	self.statusView = t;

	[root addSubview:self.setupView];
	[root addSubview:self.statusView];
	self.statusView.hidden = YES;
}

- (void)showWindow {
	if (!self.win) [self buildWindow];
	[self refresh:nil];
	[NSApp activateIgnoringOtherApps:YES];
	[self.win makeKeyAndOrderFront:nil];
}

- (NSDictionary *)state {
	char *s = goWinState();
	if (!s) return @{};
	NSData *d = [NSData dataWithBytes:s length:strlen(s)];
	free(s);
	return [NSJSONSerialization JSONObjectWithData:d options:0 error:nil] ?: @{};
}

- (void)refresh:(id)t {
	char *s = goAppStatus();
	if (s) { self.statusLine.title = [NSString stringWithUTF8String:s]; free(s); }
	if (!self.win || !self.win.isVisible) return;
	NSDictionary *st = [self state];
	NSString *status = st[@"status"];
	BOOL paired = ![status isEqualToString:@"unpaired"];
	self.setupView.hidden = paired;
	self.statusView.hidden = !paired;
	if (!paired) {
		if (!self.prefilled) {
			self.fServer.stringValue = st[@"defaultServer"] ?: @"";
			self.fName.stringValue = st[@"hostname"] ?: @"";
			self.prefilled = YES;
		}
		NSString *m = st[@"error"];
		if (!self.busy && m.length) self.setupErr.stringValue = m;
		return;
	}
	self.prefilled = NO;
	NSDictionary *names = @{@"connecting": @"Verbindet …", @"pending": @"Wartet auf Freigabe", @"online": @"Verbunden – Zugriff möglich", @"error": @"Keine Verbindung"};
	NSDictionary *cols = @{@"online": NSColor.systemGreenColor, @"error": NSColor.systemRedColor, @"pending": NSColor.systemOrangeColor};
	self.stText.stringValue = names[status] ?: status;
	NSColor *dc = cols[status]; self.dot.layer.backgroundColor = (dc ?: NSColor.systemGrayColor).CGColor;
	NSString *code = st[@"code"];
	BOOL pend = code.length > 0;
	self.stHint.hidden = !pend;
	self.stCode.hidden = !pend;
	self.stCode.stringValue = pend ? code : @"";
	self.stInfo.stringValue = [NSString stringWithFormat:@"Benutzer: %@\nServer: %@", st[@"username"] ?: @"", st[@"server"] ?: @""];
	self.stErr.stringValue = [status isEqualToString:@"error"] ? (st[@"error"] ?: @"") : @"";
	self.cAuto2.state = [st[@"autostart"] boolValue] ? NSControlStateValueOn : NSControlStateValueOff;
}

- (void)doPair:(id)s {
	if (self.busy) return;
	self.busy = YES;
	self.goBtn.enabled = NO;
	self.goBtn.title = @"Verbinde …";
	self.setupErr.stringValue = @"";
	NSString *sv = self.fServer.stringValue, *us = self.fUser.stringValue, *nm = self.fName.stringValue;
	int au = self.cAuto.state == NSControlStateValueOn;
	dispatch_async(dispatch_get_global_queue(QOS_CLASS_USER_INITIATED, 0), ^{
		char *e = goWinPair(sv.UTF8String, us.UTF8String, nm.UTF8String, au);
		NSString *err = e ? [NSString stringWithUTF8String:e] : nil;
		if (e) free(e);
		dispatch_async(dispatch_get_main_queue(), ^{
			self.busy = NO;
			self.goBtn.enabled = YES;
			self.goBtn.title = @"Verbinden";
			self.setupErr.stringValue = err ?: @"";
			[self refresh:nil];
		});
	});
}

- (void)doUnpair:(id)s {
	NSAlert *a = [[NSAlert alloc] init];
	a.messageText = @"Zugriff widerrufen?";
	a.informativeText = @"Dieser Computer ist danach nicht mehr aus der Ferne erreichbar, bis du ihn neu einrichtest.";
	[a addButtonWithTitle:@"Widerrufen"];
	[a addButtonWithTitle:@"Abbrechen"];
	if ([a runModal] == NSAlertFirstButtonReturn) { goWinUnpair(); self.fUser.stringValue = @""; [self refresh:nil]; }
}

- (void)toggleAuto:(id)s { goWinAutostart(self.cAuto2.state == NSControlStateValueOn); }

- (void)setupMenus {
	// Hauptmenü mit Bearbeiten-Einträgen: ohne sie funktionieren Cmd+V/C/X/A in den Textfeldern nicht.
	NSMenu *bar = [[NSMenu alloc] init];
	NSMenuItem *appItem = [[NSMenuItem alloc] init];
	[bar addItem:appItem];
	NSMenu *appMenu = [[NSMenu alloc] init];
	[appMenu addItemWithTitle:@"remoteMe beenden" action:@selector(quit:) keyEquivalent:@"q"].target = self;
	appItem.submenu = appMenu;
	NSMenuItem *editItem = [[NSMenuItem alloc] init];
	[bar addItem:editItem];
	NSMenu *edit = [[NSMenu alloc] initWithTitle:@"Bearbeiten"];
	[edit addItemWithTitle:@"Ausschneiden" action:@selector(cut:) keyEquivalent:@"x"];
	[edit addItemWithTitle:@"Kopieren" action:@selector(copy:) keyEquivalent:@"c"];
	[edit addItemWithTitle:@"Einsetzen" action:@selector(paste:) keyEquivalent:@"v"];
	[edit addItemWithTitle:@"Alles auswählen" action:@selector(selectAll:) keyEquivalent:@"a"];
	editItem.submenu = edit;
	NSApp.mainMenu = bar;

	self.item = [[NSStatusBar systemStatusBar] statusItemWithLength:NSVariableStatusItemLength];
	NSImage *img = [NSImage imageWithSystemSymbolName:@"display" accessibilityDescription:@"remoteMe"];
	if (img) { img.template = YES; self.item.button.image = img; }
	else self.item.button.title = @"rM";
	NSMenu *m = [[NSMenu alloc] init];
	self.statusLine = [m addItemWithTitle:@"remoteMe" action:nil keyEquivalent:@""];
	self.statusLine.enabled = NO;
	[m addItem:[NSMenuItem separatorItem]];
	[m addItemWithTitle:@"Einstellungen …" action:@selector(openUI:) keyEquivalent:@","].target = self;
	[m addItemWithTitle:@"Berechtigungen prüfen …" action:@selector(perms:) keyEquivalent:@""].target = self;
	[m addItem:[NSMenuItem separatorItem]];
	[m addItemWithTitle:@"remoteMe beenden" action:@selector(quit:) keyEquivalent:@"q"].target = self;
	self.item.menu = m;
	[NSTimer scheduledTimerWithTimeInterval:1 target:self selector:@selector(refresh:) userInfo:nil repeats:YES];
}

- (void)openUI:(id)s { [self showWindow]; }
- (void)perms:(id)s { fwRequestPermissions(); }
- (void)quit:(id)s { goAppQuit(); }
// Erneutes Öffnen der App (Finder/Launchpad) zeigt das Fenster.
- (BOOL)applicationShouldHandleReopen:(NSApplication *)a hasVisibleWindows:(BOOL)f { [self showWindow]; return YES; }
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

void fwShowWindow(void) {
	dispatch_async(dispatch_get_main_queue(), ^{ [gApp showWindow]; });
}

void fwRunApp(int showWindow) {
	@autoreleasepool {
		NSApplication *app = [NSApplication sharedApplication];
		[app setActivationPolicy:NSApplicationActivationPolicyAccessory];
		gApp = [[FWApp alloc] init];
		app.delegate = gApp;
		[gApp setupMenus];
		[gApp refresh:nil];
		if (showWindow) fwShowWindow();
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
