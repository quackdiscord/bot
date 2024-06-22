package services

import (
	"context"
	"crypto/tls"
	"os"

	"github.com/segmentio/kafka-go"
	"github.com/segmentio/kafka-go/sasl/scram"
	log "github.com/sirupsen/logrus"
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
		log.WithError(err).Fatal("Error creating Kafka SASL mechanism")
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

	log.Info("Connected to Kafka")
}

func (k *KafkaService) Produce(ctx context.Context, key, value []byte) error {
	err := k.writer.WriteMessages(ctx, kafka.Message{
		Key:   key,
		Value: value,
	})
	if err != nil {
		log.WithError(err).Error("Error producing Kafka message")
		return err
	}
	return nil
}

func DisconnectKafka() {
	if err := Kafka.writer.Close(); err != nil {
		log.WithError(err).Error("Error closing Kafka writer")
	}

	log.Info("Disconnected from Kafka")
}
