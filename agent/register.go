package main

import (
	"maa-yys-agent/registry"

	"github.com/rs/zerolog/log"
)

var (
	customActions      = registry.CustomActions
	customRecognitions = registry.CustomRecognitions
)

func registerAll() error {
	if err := registry.RegisterAgentServer(); err != nil {
		return err
	}
	log.Info().
		Int("actions", len(customActions)).
		Int("recognitions", len(customRecognitions)).
		Msg("Custom runners registered")
	return nil
}
