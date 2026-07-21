package main

import (
	"go.uber.org/zap"
	"kova/config"
	"kova/internal/deps"
	"kova/internal/server"
	"kova/log"
	"os"
)

func main() {
	log.InitLogger()
	defer log.GetLogger().Sync()

	var err error
	if !config.LoadConfig() {
		return
	}

	if err = config.CheckConfig(); err != nil {
		log.GetLogger().Error("Invalid Kova configuration", zap.Error(err))
		return
	}

	if err = deps.CheckDependency(); err != nil {
		log.GetLogger().Error("Failed to prepare Kova dependencies", zap.Error(err))
		return
	}
	if err = server.StartBackend(); err != nil {
		log.GetLogger().Error("Failed to start Kova server", zap.Error(err))
		os.Exit(1)
	}
}
