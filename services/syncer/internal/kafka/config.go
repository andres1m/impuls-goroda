package kafka

import "errors"

type Config struct {
	Brokers       []string `yaml:"brokers"`
	RawTopic      string   `yaml:"raw-topic"`
	RawPartitions int32    `yaml:"raw-partitions"`
	ConsumerGroup string   `yaml:"consumer-group"`
}

func (c Config) Validate() error {
	if len(c.Brokers) == 0 || c.RawTopic == "" || c.RawPartitions < 1 || c.ConsumerGroup == "" {
		return errors.New("kafka config needs brokers, raw-topic, raw-partitions >= 1 and consumer-group")
	}
	return nil
}
