//go:build darwin && cgo && !nodesktop

package desktop

/*
#cgo CFLAGS: -x objective-c -fobjc-arc
#cgo LDFLAGS: -framework Cocoa
#import <Cocoa/Cocoa.h>

static NSMenuItem *dkItem(NSMenu *m, NSString *title, SEL action, NSString *key, NSEventModifierFlags mods) {
	NSMenuItem *it = [m addItemWithTitle:title action:action keyEquivalent:key];
	if (mods) [it setKeyEquivalentModifierMask:mods];
	return it;
}

// A standard menu bar: without an Edit menu, Cocoa never routes ⌘C/⌘V/⌘X/⌘A/⌘Z
// to the web view, so copy & paste would not work in the app's text fields.
static void dkSetupMenu(void) {
	NSApplication *app = [NSApplication sharedApplication];
	NSMenu *bar = [[NSMenu alloc] init];

	NSMenuItem *appItem = [[NSMenuItem alloc] init];
	[bar addItem:appItem];
	NSMenu *appMenu = [[NSMenu alloc] initWithTitle:@"devknife"];
	dkItem(appMenu, @"About devknife", @selector(orderFrontStandardAboutPanel:), @"", 0);
	[appMenu addItem:[NSMenuItem separatorItem]];
	dkItem(appMenu, @"Hide devknife", @selector(hide:), @"h", 0);
	dkItem(appMenu, @"Hide Others", @selector(hideOtherApplications:), @"h", NSEventModifierFlagCommand | NSEventModifierFlagOption);
	dkItem(appMenu, @"Show All", @selector(unhideAllApplications:), @"", 0);
	[appMenu addItem:[NSMenuItem separatorItem]];
	// stop: ends the run loop so webview.Run returns and the app saves pending
	// settings before exiting; terminate: would exit() on the spot.
	[dkItem(appMenu, @"Quit devknife", @selector(stop:), @"q", 0) setTarget:app];
	[appItem setSubmenu:appMenu];

	NSMenuItem *editItem = [[NSMenuItem alloc] init];
	[bar addItem:editItem];
	NSMenu *edit = [[NSMenu alloc] initWithTitle:@"Edit"];
	dkItem(edit, @"Undo", @selector(undo:), @"z", 0);
	dkItem(edit, @"Redo", @selector(redo:), @"z", NSEventModifierFlagCommand | NSEventModifierFlagShift);
	[edit addItem:[NSMenuItem separatorItem]];
	dkItem(edit, @"Cut", @selector(cut:), @"x", 0);
	dkItem(edit, @"Copy", @selector(copy:), @"c", 0);
	dkItem(edit, @"Paste", @selector(paste:), @"v", 0);
	dkItem(edit, @"Select All", @selector(selectAll:), @"a", 0);
	[editItem setSubmenu:edit];

	NSMenuItem *winItem = [[NSMenuItem alloc] init];
	[bar addItem:winItem];
	NSMenu *win = [[NSMenu alloc] initWithTitle:@"Window"];
	dkItem(win, @"Minimize", @selector(performMiniaturize:), @"m", 0);
	dkItem(win, @"Zoom", @selector(performZoom:), @"", 0);
	[win addItem:[NSMenuItem separatorItem]];
	dkItem(win, @"Close Window", @selector(performClose:), @"w", 0);
	[winItem setSubmenu:win];
	[app setWindowsMenu:win];

	[app setMainMenu:bar];
	[app setActivationPolicy:NSApplicationActivationPolicyRegular];
	[app activateIgnoringOtherApps:YES];
}
*/
import "C"

func setupMenu() { C.dkSetupMenu() }
