//go:build darwin

package main

/*
#cgo CFLAGS: -fobjc-arc -mmacosx-version-min=13.0
#cgo LDFLAGS: -mmacosx-version-min=13.0 -framework Cocoa -framework WebKit -framework Security
#include <stdlib.h>
#include "app_darwin.h"
*/
import "C"

import "encoding/json"

func cjson(v any) *C.char {
	b, _ := json.Marshal(v)
	return C.CString(string(b))
}

func cerr(err error) *C.char {
	if err == nil {
		return nil
	}
	return C.CString(err.Error())
}

//export goRemoteInfo
func goRemoteInfo() *C.char {
	server, user, ok := acct.state()
	if !ok {
		f := loadAccount()
		server, user = f.Server, f.User
	}
	return cjson(map[string]any{"server": server, "user": user, "loggedIn": ok, "defaultServer": defaultServer})
}

//export goRemoteLogin
func goRemoteLogin(server, user, pw *C.char) *C.char {
	return cerr(acct.login(C.GoString(server), C.GoString(user), C.GoString(pw)))
}

//export goRemoteLogout
func goRemoteLogout() { acct.logout() }

//export goRemoteDevices
func goRemoteDevices() *C.char {
	l, err := acct.devices()
	if err != nil {
		return cjson(map[string]any{"error": err.Error()})
	}
	if l == nil {
		l = []remoteDevice{}
	}
	return cjson(map[string]any{"devices": l})
}

//export goRemoteApprove
func goRemoteApprove(id, code *C.char) *C.char {
	return cerr(acct.approve(C.GoString(id), C.GoString(code)))
}

//export goRemoteRemove
func goRemoteRemove(id *C.char) *C.char { return cerr(acct.remove(C.GoString(id))) }

//export goRemoteCookie
func goRemoteCookie() *C.char {
	name, val, host, secure, err := acct.sessionCookie()
	if err != nil {
		return cjson(map[string]any{"error": err.Error()})
	}
	return cjson(map[string]any{"name": name, "value": val, "host": host, "secure": secure})
}
