package main

import (
	"flag"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"

	"github.com/mgutz/logxi/v1"

	MQTT "github.com/eclipse/paho.mqtt.golang"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/prometheus/client_golang/prometheus/promhttp"
)

func main() {

	broker := flag.String("broker", "tcp://127.0.0.1:1883", "MQTT broker address including scheme and port")
	clientID := flag.String("clientID", "mqtt-hackathon-monitoring", "the client ID of the MQTT client")
	qos := flag.Int("qos", 0, "quality of service level: 0, 1 or 2")
	topics := flag.String("topics", "home/garden/fountain,home/garden/sprinkler", "comma separated list of topics to subscribe")
	flag.Parse()

	topicArr := strings.Split(*topics, ",")
	log.Info("got list of topics", "topicList", *topics, "topicCount", len(topicArr))

	messageCounter := prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "mqtt_message_counter",
			Help: "counts all messages per topic",
		},
		[]string{"topic"},
	)
	prometheus.MustRegister(messageCounter)

	opts := MQTT.NewClientOptions()
	opts.AddBroker(*broker)
	opts.SetClientID(*clientID)

	sigChan := make(chan os.Signal, 1)
	quitChan := make(chan struct{}, 1)
	messages := make(chan [2]string)

	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		sig := <-sigChan
		log.Warn("received system call", "signal", sig)
		close(quitChan)
	}()

	opts.SetDefaultPublishHandler(func(client MQTT.Client, msg MQTT.Message) {
		messages <- [2]string{msg.Topic(), string(msg.Payload())}
	})

	client := MQTT.NewClient(opts)
	if token := client.Connect(); token.Wait() && token.Error() != nil {
		log.Error("failed to create MQTT client", "error", err.Error())
		os.Exit(1)
	}

	wt := sync.WaitGroup{}
	for _, topic := range topicArr {

		go func(topic string) {

			if token := client.Subscribe(topic, byte(*qos), nil); token.Wait() && token.Error() != nil {
				log.Error("failed to subscribe to topic", "topic", topic, "error", err.Error())
				os.Exit(2)
			}
			log.Info("successfully subscribed to topic", "topic", topic)

			for {
				select {
				case message := <-messages:
					log.Info("received message", "topic", message[0], "message", message[1])
					messageCounter.WithLabelValues(topic).Inc()
				case <-quitChan:
					log.Warn("unsubscribing from topic because of quit signal", "topic", topic)
					client.Unsubscribe(topic)
					wt.Done()
					return
				}
			}

		}(topic)

		wt.Add(1)

	}

	server := &http.Server{Addr: "0.0.0.0:8080"}
	http.Handle("/metrics", promhttp.Handler())
	go func() {
		if err := server.ListenAndServe(); err != nil {
			log.Error("stopped server", "error", err.Error())
		}
		wt.Done()
	}()
	wt.Add(1)

	<-quitChan

	log.Warn("shutting down server")
	server.Shutdown(nil)
	log.Warn("waiting for go routines to complete")
	wt.Wait()
	client.Disconnect(250)
	log.Info("successfully shut down")

}
