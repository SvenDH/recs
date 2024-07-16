package world

import (
	"math/rand"
)

type Rand struct {
	rand.Source
}

type Tick struct {
	Tick int64
}

type Termination struct {
	Terminate bool
}
