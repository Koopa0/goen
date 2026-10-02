package main

import "log/slog"

// quietLog is the logger a test hands a pool whose slow-query lines it does not read.
var quietLog = slog.New(slog.DiscardHandler)
