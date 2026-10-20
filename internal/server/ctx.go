package server

import "context"

type contextT = context.Context

func contextBG() context.Context { return context.Background() }
