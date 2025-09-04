package signals

import (
	"os"
	"os/signal"
	"syscall"

	"github.com/mudler/LocalAI/pkg/model"
	"github.com/rs/zerolog/log"
)

func Handler(m *model.ModelLoader) {
	// Catch signals from the OS requesting us to exit, and stop all backends
	go func(m *model.ModelLoader) {
		c := make(chan os.Signal, 1) // we need to reserve to buffer size 1, so the notifier are not blocked
		signal.Notify(c, os.Interrupt, syscall.SIGTERM, syscall.SIGINT)
		<-c
		if m != nil {
			if err := m.StopAllGRPC(); err != nil {
				log.Error().Err(err).Msg("error while stopping all grpc backends")
			}
		}
		os.Exit(1)
	}(m)
}
