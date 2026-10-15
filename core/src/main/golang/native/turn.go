package main

//#include "bridge.h"
import "C"

import (
	"cfa/native/turn"
)

// The TURN clients of the active profile are started and stopped with the profile itself (see
// native/turn and the hooks in native/config). These three exports are the manual controls:
// they exist so the client can be driven from the app or a test without loading a profile,
// and so the bridge has somewhere to ask what the clients are doing.
//
// All three return a string, never NULL: the JNI helper turns a NULL into a strlen() crash.
// An empty string means success for the two that can fail.

//export turnStart
func turnStart(paramsJson C.c_string) *C.char {
	defer guard("turnStart")()

	if err := turn.ManagerFor().StartFromParams(C.GoString(paramsJson)); err != nil {
		return marshalString(err)
	}

	return marshalString("")
}

//export turnStop
func turnStop() *C.char {
	defer guard("turnStop")()

	turn.ManagerFor().Stop()

	return marshalString("")
}

//export turnStatus
func turnStatus() *C.char {
	defer guard("turnStatus")()

	return marshalString(turn.ManagerFor().StatusJSON())
}
