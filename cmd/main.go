package main

import (
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/milossdjuric/rolling_update_service/internal/configs"
	"github.com/milossdjuric/rolling_update_service/internal/startup"
)

func main() {

	config, err := configs.NewFromEnv()
	if err != nil {
		log.Fatalf("Error loading config: %v", err)
	}

	app, err := startup.NewAppWithConfig(config)
	if err != nil {
		log.Fatalf("Error creating app: %v", err)
	}

	err = app.Start()
	if err != nil {
		log.Fatalf("Error starting app: %v", err)
	}

	shutdown := make(chan os.Signal, 1)
	signal.Notify(shutdown, syscall.SIGTERM, syscall.SIGINT)

	<-shutdown

	log.Println("Shutting down app")

	app.Shutdown()
	log.Println("App shutdown complete")
}
