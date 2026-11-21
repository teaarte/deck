package w

import "deck2/internal/fault"

var a = fault.New(fault.CallRefused, "deck: no")
var b = fault.New("call.gone", "deck: over")
var c = fault.New(fault.Network, "the network")
var d = "a call, made"
