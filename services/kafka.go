package services

import (
	"context"
	"crypto/tls"
	"os"

	"github.com/quackdiscord/bot/log"
	"github.com/segmentio/kafka-go"
	"github.com/segmentio/kafka-go/sasl/scram"
)

var Kafka *KafkaService

type KafkaService struct {
	writer *kafka.Writer
}

func ConnectKafka() {
	broker := os.Getenv("KAFKA_BROKER")
	username := os.Getenv("KAFKA_USERNAME")
	password := os.Getenv("KAFKA_PASSWORD")
	topic := os.Getenv("KAFKA_TOPIC")
	mechanism, err := scram.Mechanism(scram.SHA256, username, password)
	if err != nil {
		log.Fatal().AnErr("Error creating Kafka SASL mechanism", err)
	}

	Kafka = &KafkaService{
		writer: &kafka.Writer{
			Addr:  kafka.TCP(broker),
			Topic: topic,
			Transport: &kafka.Transport{
				SASL: mechanism,
				TLS:  &tls.Config{},
			},
		},
	}

	log.Info().Msg("Connected to Kafka")
}

func (k *KafkaService) Produce(ctx context.Context, key, value []byte) error {
	err := k.writer.WriteMessages(ctx, kafka.Message{
		Key:   key,
		Value: value,
	})
	if err != nil {
		log.Error().AnErr("Error producing Kafka message", err)
		return err
	}
	return nil
}

func DisconnectKafka() {
	if err := Kafka.writer.Close(); err != nil {
		log.Error().AnErr("Error closing Kafka writer", err)
	}

	log.Info().Msg("Disconnected from Kafka")
}
