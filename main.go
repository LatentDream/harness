package main

import (
	"context"
	"fmt"
	"os"

	"latentdream/harness/config"
	"latentdream/harness/logging"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to load config: %v\n", err)
		os.Exit(1)
	}

	if err := logging.Configure(cfg.Logging); err != nil {
		fmt.Fprintf(os.Stderr, "failed to configure logging: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("Harness")
	logging.Log(context.Background()).Info("Hello World")
}
