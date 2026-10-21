#import <Cocoa/Cocoa.h>
#import <WebKit/WebKit.h>
#import <Security/Security.h>
#include "app_darwin.h"

// Von Go bereitgestellt (remote_darwin.go); Rückgabe-Strings mit free() freigeben.
extern char *goRemoteInfo(void);
extern char *goRemoteLogin(const char *server, const char *user, const char *pw);
extern void goRemoteLogout(void);
extern char *goRemoteDevices(void);
extern char *goRemoteApprove(const char *id, const char *code);
extern char *goRemoteRemove(const char *id);
extern char *goRemoteCookie(void);

static NSDictionary *jsonOf(char *s) {
	if (!s) return @{};
	NSData *d = [NSData dataWithBytes:s length:strlen(s)];
	free(s);
	return [NSJSONSerialization JSONObjectWithData:d options:0 error:nil] ?: @{};
}
static NSString *strOf(char *s) {
	if (!s) return nil;
	NSString *r = [NSString stringWithUTF8String:s];
	free(s);
	return r;
}

// ---- Schlüsselbund: Passwort je Benutzer@Server ----
static NSDictionary *kcQuery(NSString *acct) {
	return @{(__bridge id)kSecClass: (__bridge id)kSecClassGenericPassword,
	         (__bridge id)kSecAttrService: @"remoteMe",
	         (__bridge id)kSecAttrAccount: acct};
}
static NSString *kcGet(NSString *acct) {
	NSMutableDictionary *q = [kcQuery(acct) mutableCopy];
	q[(__bridge id)kSecReturnData] = @YES;
	CFTypeRef out = NULL;
	if (SecItemCopyMatching((__bridge CFDictionaryRef)q, &out) != errSecSuccess || !out) return nil;
	NSData *d = (__bridge_transfer NSData *)out;
	return [[NSString alloc] initWithData:d encoding:NSUTF8StringEncoding];
}
static void kcSet(NSString *acct, NSString *pw) {
	SecItemDelete((__bridge CFDictionaryRef)kcQuery(acct));
	NSMutableDictionary *q = [kcQuery(acct) mutableCopy];
	q[(__bridge id)kSecValueData] = [pw dataUsingEncoding:NSUTF8StringEncoding];
	SecItemAdd((__bridge CFDictionaryRef)q, NULL);
}
static void kcDel(NSString *acct) { SecItemDelete((__bridge CFDictionaryRef)kcQuery(acct)); }

static NSTextField *lbl(NSString *t, NSRect f, CGFloat size, BOOL bold) {
	NSTextField *l = [NSTextField labelWithString:t];
	l.frame = f;
	l.font = bold ? [NSFont boldSystemFontOfSize:size] : [NSFont systemFontOfSize:size];
	return l;
}

// WKWebView, das Cmd-Kombinationen an die Seite (und damit an den entfernten Rechner) weitergibt.
@interface FWWebView : WKWebView
@end
@implementation FWWebView
- (BOOL)performKeyEquivalent:(NSEvent *)e {
	if (e.modifierFlags & NSEventModifierFlagCommand) {
		NSString *k = e.charactersIgnoringModifiers.lowercaseString;
		if ([@[@"q", @"w", @"m", @"h", @"`", @","] containsObject:k]) return [super performKeyEquivalent:e];
		[self keyDown:e];
		return YES;
	}
	return [super performKeyEquivalent:e];
}
@end

@interface FWRemote : NSObject <NSTableViewDataSource, NSTableViewDelegate, NSWindowDelegate>
@property(strong) NSWindow *win;
@property(strong) NSView *loginView, *listView;
@property(strong) NSTextField *fServer, *fUser, *fPass, *loginErr, *whoLabel, *listErr;
@property(strong) NSButton *loginBtn, *connectBtn, *approveBtn, *removeBtn;
@property(strong) NSTableView *table;
@property(strong) NSArray<NSDictionary *> *devs;
@property(strong) NSMutableArray *sessions;
@property(assign) BOOL busy, polling;
@end

@implementation FWRemote

#define RW 560
#define RH 440

- (void)build {
	self.sessions = [NSMutableArray array];
	self.devs = @[];
	self.win = [[NSWindow alloc] initWithContentRect:NSMakeRect(0, 0, RW, RH)
		styleMask:NSWindowStyleMaskTitled | NSWindowStyleMaskClosable | NSWindowStyleMaskMiniaturizable
		backing:NSBackingStoreBuffered defer:NO];
	self.win.title = @"remoteMe – Meine Computer";
	self.win.releasedWhenClosed = NO;
	[self.win center];
	NSView *root = self.win.contentView;

	// ---- Anmeldung ----
	NSView *a = [[NSView alloc] initWithFrame:NSMakeRect(0, 0, RW, RH)];
	[a addSubview:lbl(@"Auf Computer zugreifen", NSMakeRect(28, RH - 62, RW - 56, 26), 20, YES)];
	NSTextField *d = [NSTextField wrappingLabelWithString:@"Melde dich an deinem remoteMe-Server an. Danach siehst du alle freigegebenen Computer und verbindest dich ohne Code."];
	d.frame = NSMakeRect(28, RH - 112, RW - 56, 44); d.font = [NSFont systemFontOfSize:12]; d.textColor = NSColor.secondaryLabelColor;
	[a addSubview:d];
	CGFloat y = RH - 142;
	[a addSubview:lbl(@"Server", NSMakeRect(28, y, RW - 56, 16), 12, YES)];
	self.fServer = [[NSTextField alloc] initWithFrame:NSMakeRect(28, y - 28, RW - 56, 24)];
	self.fServer.placeholderString = @"https://remoteme.example.com";
	[a addSubview:self.fServer];
	y -= 62;
	[a addSubview:lbl(@"Benutzername", NSMakeRect(28, y, RW - 56, 16), 12, YES)];
	self.fUser = [[NSTextField alloc] initWithFrame:NSMakeRect(28, y - 28, RW - 56, 24)];
	[a addSubview:self.fUser];
	y -= 62;
	[a addSubview:lbl(@"Passwort", NSMakeRect(28, y, RW - 56, 16), 12, YES)];
	self.fPass = [[NSSecureTextField alloc] initWithFrame:NSMakeRect(28, y - 28, RW - 56, 24)];
	[a addSubview:self.fPass];
	self.loginErr = [NSTextField wrappingLabelWithString:@""];
	self.loginErr.frame = NSMakeRect(28, 62, RW - 56, 34); self.loginErr.font = [NSFont systemFontOfSize:12]; self.loginErr.textColor = NSColor.systemRedColor;
	[a addSubview:self.loginErr];
	self.loginBtn = [NSButton buttonWithTitle:@"Anmelden" target:self action:@selector(doLogin:)];
	self.loginBtn.frame = NSMakeRect(RW - 28 - 120, 20, 120, 32); self.loginBtn.keyEquivalent = @"\r";
	[a addSubview:self.loginBtn];
	self.loginView = a;

	// ---- Liste ----
	NSView *l = [[NSView alloc] initWithFrame:NSMakeRect(0, 0, RW, RH)];
	[l addSubview:lbl(@"Meine Computer", NSMakeRect(28, RH - 58, RW - 56, 26), 20, YES)];
	NSScrollView *sc = [[NSScrollView alloc] initWithFrame:NSMakeRect(28, 100, RW - 56, RH - 170)];
	sc.hasVerticalScroller = YES; sc.borderType = NSBezelBorder;
	self.table = [[NSTableView alloc] initWithFrame:sc.bounds];
	for (NSArray *c in @[@[@"name", @"Computer", @210], @[@"os", @"System", @90], @[@"st", @"Status", @160]]) {
		NSTableColumn *col = [[NSTableColumn alloc] initWithIdentifier:c[0]];
		col.title = c[1]; col.width = [c[2] doubleValue];
		[self.table addTableColumn:col];
	}
	self.table.dataSource = self; self.table.delegate = self;
	self.table.target = self; self.table.doubleAction = @selector(doConnect:);
	sc.documentView = self.table;
	[l addSubview:sc];
	self.listErr = [NSTextField wrappingLabelWithString:@""];
	self.listErr.frame = NSMakeRect(28, 66, RW - 56, 28); self.listErr.font = [NSFont systemFontOfSize:12]; self.listErr.textColor = NSColor.systemRedColor;
	[l addSubview:self.listErr];
	self.connectBtn = [NSButton buttonWithTitle:@"Verbinden" target:self action:@selector(doConnect:)];
	self.connectBtn.frame = NSMakeRect(RW - 28 - 110, 20, 110, 32); self.connectBtn.keyEquivalent = @"\r";
	[l addSubview:self.connectBtn];
	self.approveBtn = [NSButton buttonWithTitle:@"Freigeben …" target:self action:@selector(doApprove:)];
	self.approveBtn.frame = NSMakeRect(RW - 28 - 110 - 8 - 110, 20, 110, 32);
	[l addSubview:self.approveBtn];
	self.removeBtn = [NSButton buttonWithTitle:@"Entfernen" target:self action:@selector(doRemove:)];
	self.removeBtn.frame = NSMakeRect(RW - 28 - 330 - 16, 20, 100, 32);
	[l addSubview:self.removeBtn];
	NSButton *out = [NSButton buttonWithTitle:@"Abmelden" target:self action:@selector(doLogout:)];
	out.frame = NSMakeRect(28, 20, 100, 32);
	[l addSubview:out];
	self.whoLabel = lbl(@"", NSMakeRect(28, RH - 78, RW - 56, 16), 12, NO);
	self.whoLabel.textColor = NSColor.secondaryLabelColor;
	[l addSubview:self.whoLabel];
	self.listView = l;

	[root addSubview:self.loginView];
	[root addSubview:self.listView];
	self.listView.hidden = YES;
	[NSTimer scheduledTimerWithTimeInterval:3 target:self selector:@selector(poll:) userInfo:nil repeats:YES];
}

- (void)show {
	if (!self.win) [self build];
	[NSApp activateIgnoringOtherApps:YES];
	[self.win makeKeyAndOrderFront:nil];
	NSDictionary *info = jsonOf(goRemoteInfo());
	if ([info[@"loggedIn"] boolValue]) { [self showList:info]; return; }
	NSString *server = info[@"server"] ?: @"", *user = info[@"user"] ?: @"";
	self.fServer.stringValue = server.length ? server : (info[@"defaultServer"] ?: @"");
	self.fUser.stringValue = user;
	self.loginView.hidden = NO; self.listView.hidden = YES;
	// gespeichertes Passwort -> still anmelden
	NSString *pw = (server.length && user.length) ? kcGet([NSString stringWithFormat:@"%@@%@", user, server]) : nil;
	if (pw) { self.fPass.stringValue = pw; [self doLogin:nil]; }
}

- (void)showList:(NSDictionary *)info {
	self.loginView.hidden = YES; self.listView.hidden = NO;
	self.whoLabel.stringValue = [NSString stringWithFormat:@"Angemeldet als %@ auf %@", info[@"user"] ?: @"", info[@"server"] ?: @""];
	[self poll:nil];
}

- (void)doLogin:(id)s {
	if (self.busy) return;
	self.busy = YES; self.loginBtn.enabled = NO; self.loginBtn.title = @"Melde an …"; self.loginErr.stringValue = @"";
	NSString *sv = self.fServer.stringValue, *us = self.fUser.stringValue, *pw = self.fPass.stringValue;
	dispatch_async(dispatch_get_global_queue(QOS_CLASS_USER_INITIATED, 0), ^{
		NSString *err = strOf(goRemoteLogin(sv.UTF8String, us.UTF8String, pw.UTF8String));
		dispatch_async(dispatch_get_main_queue(), ^{
			self.busy = NO; self.loginBtn.enabled = YES; self.loginBtn.title = @"Anmelden";
			if (err) { self.loginErr.stringValue = err; return; }
			NSDictionary *info = jsonOf(goRemoteInfo());
			kcSet([NSString stringWithFormat:@"%@@%@", info[@"user"], info[@"server"]], pw);
			self.fPass.stringValue = @"";
			[self showList:info];
		});
	});
}

- (void)doLogout:(id)s {
	NSDictionary *info = jsonOf(goRemoteInfo());
	kcDel([NSString stringWithFormat:@"%@@%@", info[@"user"], info[@"server"]]);
	goRemoteLogout();
	self.devs = @[]; [self.table reloadData];
	self.fPass.stringValue = @"";
	self.loginView.hidden = NO; self.listView.hidden = YES;
}

- (void)poll:(id)t {
	if (!self.win || !self.win.isVisible || self.listView.hidden || self.polling) return;
	self.polling = YES;
	dispatch_async(dispatch_get_global_queue(QOS_CLASS_UTILITY, 0), ^{
		NSDictionary *r = jsonOf(goRemoteDevices());
		dispatch_async(dispatch_get_main_queue(), ^{
			self.polling = NO;
			NSString *err = r[@"error"];
			self.listErr.stringValue = err ?: @"";
			if (err) return;
			NSString *sel = [self selected][@"id"];
			self.devs = r[@"devices"] ?: @[];
			[self.table reloadData];
			if (sel) for (NSUInteger i = 0; i < self.devs.count; i++)
				if ([self.devs[i][@"id"] isEqualToString:sel]) [self.table selectRowIndexes:[NSIndexSet indexSetWithIndex:i] byExtendingSelection:NO];
			[self updateButtons];
		});
	});
}

- (NSDictionary *)selected {
	NSInteger r = self.table.selectedRow;
	return (r >= 0 && r < (NSInteger)self.devs.count) ? self.devs[r] : nil;
}

- (void)updateButtons {
	NSDictionary *d = [self selected];
	BOOL appr = [d[@"approved"] boolValue];
	self.connectBtn.enabled = d && appr && [d[@"online"] boolValue];
	self.approveBtn.enabled = d && !appr;
	self.removeBtn.enabled = d != nil;
}

- (void)tableViewSelectionDidChange:(NSNotification *)n { [self updateButtons]; }
- (NSInteger)numberOfRowsInTableView:(NSTableView *)t { return self.devs.count; }
- (id)tableView:(NSTableView *)t objectValueForTableColumn:(NSTableColumn *)c row:(NSInteger)r {
	NSDictionary *d = self.devs[r];
	NSString *k = c.identifier;
	if ([k isEqualToString:@"name"]) return d[@"name"];
	if ([k isEqualToString:@"os"]) return d[@"os"];
	BOOL appr = [d[@"approved"] boolValue], on = [d[@"online"] boolValue], busy = [d[@"busy"] boolValue];
	NSString *txt; NSColor *col;
	if (!appr) { txt = @"Wartet auf Freigabe"; col = NSColor.systemOrangeColor; }
	else if (!on) { txt = @"Offline"; col = NSColor.systemGrayColor; }
	else if (busy) { txt = @"Online (in Benutzung)"; col = NSColor.systemGreenColor; }
	else { txt = @"Online"; col = NSColor.systemGreenColor; }
	NSMutableAttributedString *a = [[NSMutableAttributedString alloc] initWithString:@"● " attributes:@{NSForegroundColorAttributeName: col}];
	[a appendAttributedString:[[NSAttributedString alloc] initWithString:txt attributes:@{NSForegroundColorAttributeName: NSColor.labelColor}]];
	return a;
}

- (void)doApprove:(id)s {
	NSDictionary *d = [self selected];
	if (!d) return;
	NSAlert *a = [[NSAlert alloc] init];
	a.messageText = [NSString stringWithFormat:@"„%@“ freigeben", d[@"name"]];
	a.informativeText = @"Gib den 6-stelligen Code ein, der in der remoteMe-App auf diesem Computer angezeigt wird.";
	NSTextField *f = [[NSTextField alloc] initWithFrame:NSMakeRect(0, 0, 200, 24)];
	f.placeholderString = @"123456";
	a.accessoryView = f;
	[a addButtonWithTitle:@"Freigeben"]; [a addButtonWithTitle:@"Abbrechen"];
	a.window.initialFirstResponder = f;
	if ([a runModal] != NSAlertFirstButtonReturn) return;
	NSString *id_ = d[@"id"], *code = f.stringValue;
	dispatch_async(dispatch_get_global_queue(QOS_CLASS_USER_INITIATED, 0), ^{
		NSString *err = strOf(goRemoteApprove(id_.UTF8String, code.UTF8String));
		dispatch_async(dispatch_get_main_queue(), ^{
			if (err) { NSAlert *e = [[NSAlert alloc] init]; e.messageText = @"Freigabe fehlgeschlagen"; e.informativeText = err; [e runModal]; }
			[self poll:nil];
		});
	});
}

- (void)doRemove:(id)s {
	NSDictionary *d = [self selected];
	if (!d) return;
	NSAlert *a = [[NSAlert alloc] init];
	a.messageText = [NSString stringWithFormat:@"„%@“ entfernen?", d[@"name"]];
	a.informativeText = @"Der Computer wird getrennt und muss neu eingerichtet werden, um wieder erreichbar zu sein.";
	[a addButtonWithTitle:@"Entfernen"]; [a addButtonWithTitle:@"Abbrechen"];
	if ([a runModal] != NSAlertFirstButtonReturn) return;
	NSString *id_ = d[@"id"];
	dispatch_async(dispatch_get_global_queue(QOS_CLASS_USER_INITIATED, 0), ^{
		goRemoteRemove(id_.UTF8String);
		dispatch_async(dispatch_get_main_queue(), ^{ [self poll:nil]; });
	});
}

// Sitzung: eingebetteter Viewer mit der Anmeldung dieser App (kein Browser, keine Codeeingabe).
- (void)doConnect:(id)s {
	NSDictionary *d = [self selected];
	if (!d || ![d[@"approved"] boolValue] || ![d[@"online"] boolValue]) return;
	NSDictionary *info = jsonOf(goRemoteInfo());
	NSDictionary *ck = jsonOf(goRemoteCookie());
	if (!ck[@"value"]) { self.listErr.stringValue = @"Sitzung abgelaufen – bitte neu anmelden."; return; }

	WKWebViewConfiguration *cfg = [[WKWebViewConfiguration alloc] init];
	cfg.websiteDataStore = [WKWebsiteDataStore nonPersistentDataStore];
	@try { [cfg.preferences setValue:@YES forKey:@"fullScreenEnabled"]; } @catch (id e) {}
	NSMutableDictionary *p = [@{NSHTTPCookieName: ck[@"name"], NSHTTPCookieValue: ck[@"value"], NSHTTPCookieDomain: ck[@"host"],
		NSHTTPCookiePath: @"/", NSHTTPCookieExpires: [NSDate dateWithTimeIntervalSinceNow:13 * 86400]} mutableCopy];
	if ([ck[@"secure"] boolValue]) p[NSHTTPCookieSecure] = @"TRUE";
	NSHTTPCookie *cookie = [NSHTTPCookie cookieWithProperties:p];

	NSWindow *w = [[NSWindow alloc] initWithContentRect:NSMakeRect(0, 0, 1180, 760)
		styleMask:NSWindowStyleMaskTitled | NSWindowStyleMaskClosable | NSWindowStyleMaskMiniaturizable | NSWindowStyleMaskResizable
		backing:NSBackingStoreBuffered defer:NO];
	w.title = [NSString stringWithFormat:@"%@ – remoteMe", d[@"name"]];
	w.releasedWhenClosed = NO; w.delegate = self; w.minSize = NSMakeSize(640, 420);
	[w center];
	FWWebView *wv = [[FWWebView alloc] initWithFrame:w.contentView.bounds configuration:cfg];
	wv.autoresizingMask = NSViewWidthSizable | NSViewHeightSizable;
	[w.contentView addSubview:wv];
	[self.sessions addObject:w];
	NSString *base = info[@"server"];
	NSString *name = [d[@"name"] stringByAddingPercentEncodingWithAllowedCharacters:NSCharacterSet.alphanumericCharacterSet] ?: @"";
	NSURL *url = [NSURL URLWithString:[NSString stringWithFormat:@"%@/#app=1&d=%@&n=%@", base, d[@"id"], name]];
	[cfg.websiteDataStore.httpCookieStore setCookie:cookie completionHandler:^{
		[wv loadRequest:[NSURLRequest requestWithURL:url]];
	}];
	[NSApp activateIgnoringOtherApps:YES];
	[w makeKeyAndOrderFront:nil];
	[w makeFirstResponder:wv];
}

- (void)windowWillClose:(NSNotification *)n {
	NSWindow *w = n.object;
	if (![self.sessions containsObject:w]) return;
	for (NSView *v in w.contentView.subviews) if ([v isKindOfClass:WKWebView.class]) {
		[(WKWebView *)v loadHTMLString:@"" baseURL:nil]; // Seite verlassen -> Sitzung wird sauber beendet
		[v removeFromSuperview];
	}
	[self.sessions removeObject:w];
}
@end

static FWRemote *gRemote;

void fwRemoteShow(void) {
	dispatch_async(dispatch_get_main_queue(), ^{
		if (!gRemote) gRemote = [[FWRemote alloc] init];
		[gRemote show];
	});
}
