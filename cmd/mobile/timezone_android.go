//go:build android

package main

/*
#include <sys/system_properties.h>
#include <stdlib.h>
*/
import "C"

import (
	"time"
	"unsafe"
)

func initAndroidTimeZone() {
	cname := C.CString("persist.sys.timezone")
	defer C.free(unsafe.Pointer(cname))

	var buf [128]C.char

	n := C.__system_property_get(cname, &buf[0])
	if n <= 0 {
		return
	}

	tz := C.GoStringN(&buf[0], C.int(n))

	loc, err := time.LoadLocation(tz)
	if err != nil {
		return
	}

	time.Local = loc
}
