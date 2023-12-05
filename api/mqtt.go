package api

import (
	"fmt"

	mqtt "github.com/eclipse/paho.mqtt.golang"
	config "github.com/go-skynet/LocalAI/api/config"
	"github.com/go-skynet/LocalAI/api/options"
)

//

func MqttStartup(options *options.Option, cl *config.ConfigLoader) {

}

func MqttListener(opts ...options.AppOption) (*mqtt.Client, error) {

	return nil, fmt.Errorf("oops")
}
