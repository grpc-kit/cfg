package cfg

import (
	"io"
	"time"
)

// SaramaConfig 用于kafka客户端配置，结构等同于sarama类库
// https://pkg.go.dev/github.com/Shopify/sarama#Config
type SaramaConfig struct {
	Net struct {
		// 默认：5
		MaxOpenRequests int `mapstructure:"max_open_requests"`

		// 以下默认：30s
		DialTimeout  time.Duration `mapstructure:"dial_timeout"`
		ReadTimeout  time.Duration `mapstructure:"read_timeout"`
		WriteTimeout time.Duration `mapstructure:"write_timeout"`

		TLS struct {
			Enable bool `mapstructure:"enable"`
		} `mapstructure:"tls"`

		SASL struct {
			Enable    bool   `mapstructure:"enable"`
			Mechanism string `mapstructure:"mechanism"`
			User      string `mapstructure:"user"`
			Password  string `mapstructure:"password"`
		} `mapstructure:"sasl"`

		KeepAlive time.Duration `mapstructure:"keep_alive"`
	} `mapstructure:"net"`

	Metadata struct {
		// 获取元数据的策略
		Retry struct {
			// 当集群处于leader选举时最大重试次数，默认：3
			Max int `mapstructure:"max"`
			// 当集群处于leader选举重试的等扽时间，默认：250ms
			Backoff time.Duration `mapstructure:"backoff"`
		} `mapstructure:"retry"`

		// 后台与集群同步metadata的间隔，默认: 10m
		RefreshFrequency time.Duration `mapstructure:"refresh_frequency"`

		// 是否为所有topic维护元数据，默认: true
		Full bool `mapstructure:"full"`

		// 等待metadata响应的超时时间，默认禁用表示失败则继续重试
		// Net.[Dial|Read]Timeout * BrokerCount * (Metadata.Retry.Max + 1) + Metadata.Retry.Backoff * Metadata.Retry.Max
		Timeout time.Duration `mapstructure:"timeout"`

		// 如果提供的topic不存在是否允许自动创建（前提是集群配置可允许该操作），默认：true
		AllowAutoTopicCreation bool `mapstructure:"allow_auto_topic_creation"`
	} `mapstructure:"metadata"`

	// 生产者相关配置
	Producer struct {
		// 允许的最大消息大小，最好等于集群配置的：message.max.bytes，默认：1000000
		MaxMessageBytes int `mapstructure:"max_message_bytes"`

		// 消息生产被集群接收的策略，主要影响是否会丢消息与性能，默认：1
		// 设置为0: 生产者不等扽集群的响应，继续下一条
		// 设置为1: 生成者等待leader响应，然后在继续下一条
		// 设置为-1: 生产者必须等待所有"in-sync"副本响应完成，继续下一条，这个副本由: min.insync.replicas 决定
		RequiredAcks int16 `mapstructure:"required_acks"`

		// 生产者等扽响应的最长时间，当RequiredAcks设置大于1时才有效，等同于`request.timeout.ms`，默认：10s
		Timeout time.Duration `mapstructure:"timeout"`

		// 生产的消息使用的压缩算法，默认不压缩，默认：0
		Compresson int8 `mapstructure:"compresson"`

		// 压缩的等级，依赖具体压缩算法
		CompressionLevel int `mapstructure:"commpression_level"`

		// Partitioner PartitionerConstructor

		// 如果启用，生产者将确保每个消息只写入一个副本。
		Idempotent bool `mapstructure:"idempotent"`

		// 消息响应成功或失败是否写入channel里，如果写入则必须被消费，否则可能出现死锁
		Return struct {
			// 成功的消息是否记录，默认：false
			Successes bool `mapstructure:"successes"`
			// 失败的消息是否记录，默认：true
			Errors bool `mapstructure:"errors"`
		} `mapstructure:"return"`

		// 生产者达到以下阈值时触发打包消息发送至集群
		Flush struct {
			// 最大值被 sarama.MaxRequestSiz 限制，值：100 * 1024 * 1024
			Bytes int `mapstructure:"bytes"`
			// 消息数量阈值，最大限制通过以下MaxMessages控制
			Messages int `mapstructure:"messages"`
			// 等待时间阈值
			Frequency time.Duration `mapstructure:"frequency"`
			// 在单一请求broker时允许的最大消息数，设置为0则不限制
			MaxMessages int `mapstructure:"max_messages"`
		} `mapstructure:"flush"`

		// 生产消息失败的重试策略
		Retry struct {
			// 最大重试次数，等同于jvm的：message.send.max.retries，默认：3
			Max int `mapstructure:"max"`
			// 重试失败之间等待间隔，等同于jvm的：retry.backoff.ms，默认值：100ms
			Backoff time.Duration `mapstructure:"backoff"`
		} `mapstructure:"retry"`
	} `mapstructure:"producer"`

	// 消费者相关配置
	Consumer struct {
		Group struct {
			Session struct {
				// 当broker端未收到消费者的心跳包，超过该时间间隔，则broker认为该消费者离线，将进行重均衡，默认：10s
				// 该值必须在broker配置`group.min.session.timeout.ms`与`group.max.session.timeout.ms`之间
				Timeout time.Duration `mapstructure:"timeout"`
			} `mapstructure:"session"`
			Heartbeat struct {
				// kafka协调者预期的心跳间隔，用于确保消费者session处于活跃状态，值必须小于session.timeout，默认：3s
				// 一般建议设置为session.timeout的3分之一
				Interval time.Duration `mapstructure:"interval"`
			} `mapstructure:"heartbeat"`
			Rebalance struct {
				// topic分区分配给消费者的策略，支持：range, roundrobin, sticky，默认：range
				// range: 标识使用范围分区分配策略的策略
				// roundrobin: 标识使用循环分区分配策略的策略
				// sticky: 标识使用粘性分区分配策略的策略
				Strategy string `mapstructure:"strategy"`
				// 重均衡开始后，消费者加入群组的最大允许时间，默认：60s
				Timeout time.Duration `mapstructure:"timeout"`

				Retry struct {
					// 最大重试次数，默认：4
					Max int `mapstructure:"max"`
					// 重试失败之间等待间隔，默认：2s
					Backoff time.Duration `mapstructure:"backoff"`
				} `mapstructure:"retry"`
			} `mapstructure:"rebalance"`
		} `mapstructure:"group"`

		// 读取分区失败的重试
		Retry struct {
			// 重试失败之间等待间隔，默认：2s
			Backoff time.Duration `mapstructure:"backoff"`
		} `mapstructure:"retry"`

		// 控制每个请求所拉取数据的大小，单位bytes
		Fetch struct {
			// 必须等待的最小消息大小，不要设置为0，等同于jvm `fetch.min.bytes`，默认：1
			Min int32 `mapstructure:"min"`
			// 每请求从broker获取的消息大小，默认：1MB
			// 尽量大于你消息的大部分大小，否则还要做额外的切割，等同于jvm `fetch.message.max.bytes`
			Default int32 `mapstructure:"default"`
			// 每请求可最大获取的消息大小，值为0表示不限制，等同于jvm `fetch.message.max.bytes`，默认：0
			Max int32 `mapstructure:"max"`
		} `mapstructure:"fetch"`

		// broker在等待消息达到 Consumer.Fetch.Min 大小的最大时间，不要设置为0，默认：250ms
		// 建议在 100-500ms，等同于jvm `fetch.wait.max.ms`
		MaxWaitTime time.Duration `mapstructure:"max_wait_time"`

		// 消费者为用户处理消息所需的最长时间，如果写入消息通道所需的时间超过此时间，则该分区将停止获取更多消息，直到可以再次继续。
		// 由于消息通道已缓冲，因此实际宽限时间为 (MaxProcessingTime * ChannelBufferSize)，默认：100ms
		MaxProcessingTime time.Duration `mapstructure:"max_processing_time"`

		// 消息响应成功或失败是否写入channel里，如果写入则必须被消费，否则可能出现死锁
		Return struct {
			// 失败的消息是否记录，默认：false
			Errors bool `mapstructure:"errors"`
		} `mapstructure:"return"`

		// 控制如何提交消费offset
		Offsets struct {
			AutoCommit struct {
				// 是否自动更新，默认：true
				Enable bool `mapstructure:"enable"`
				// 自动更新频率，默认：1s
				Interval time.Duration `mapstructure:"interval"`
			} `mapstructure:"auto_commit"`

			// 提交offset失败的重试
			Retry struct {
				// 最大重试次数，默认：3
				Max int `mapstructure:"max"`
			} `mapstructure:"retry"`
		} `mapstructure:"offsets"`

		// 消费隔离级别，ReadUncommitted 或 ReadCommitted，默认：ReadUncommitted
		// ReadUncommitted: 可以读取到未提交的数据(报错终止前的数据)
		// ReadCommitted: 生产者已提交的数据才能读取到
		IsolationLevel int8 `mapstructure:"isolation_level"`
	} `mapstructure:"consumer"`

	// 标识该消费者
	ClientID string `mapstructure:"client_id"`
	// 机柜标识，见 'broker.rack'
	RackID string `mapstructure:"rack_id"`
	// 默认：256
	ChannelBufferSize int    `mapstructure:"chnnel_buffer_size"`
	Version           string `mapstructure:"version"`
}

// InitCloudEvents 初始化cloudevents数据实例
func (c *LocalConfig) InitCloudEvents() (io.Closer, error) {
	return nil, nil
}
