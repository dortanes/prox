package main

import "os"

// restartSignals is empty: Windows cannot pass listening sockets this way.
var restartSignals []os.Signal
