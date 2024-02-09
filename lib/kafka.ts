import { Kafka, logLevel } from "kafkajs";
import config from "../config";

const kafka = new Kafka({
    brokers: config.kafka.brokers,
    ssl: true,
    sasl: {
        mechanism: "scram-sha-256",
        username: config.kafka.username,
        password: config.kafka.password
    },
    logLevel: logLevel.ERROR
});

export { kafka };
