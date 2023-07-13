package main

import (
	"fmt"
	"os"

	"github.com/bufbuild/vanguard/internal/extprocapp"
	"go.uber.org/zap"
)

func main() {
	logger, err := zap.NewProduction()
	if err != nil {
		fmt.Fprintln(os.Stderr, "failed to create logger:", err)
		os.Exit(2)
	}
	if err := extprocapp.Main(os.Args[1:]); err != nil {
		logger.Fatal("runtime error", zap.Error(err))
	}
}
