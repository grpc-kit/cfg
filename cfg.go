package cfg

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"strings"
	"time"

	"github.com/grpc-kit/pkg/sd"
	"github.com/mitchellh/mapstructure"
	"github.com/sirupsen/logrus"
	"github.com/spf13/viper"
)

// LocalConfig 本地配置，全局微服务配置结构
type LocalConfig struct {
	Services    *ServicesConfig    `json:",omitempty"` // 基础服务配置
	Discover    *DiscoverConfig    `json:",omitempty"` // 服务注册配置
	Security    *SecurityConfig    `json:",omitempty"` // 认证鉴权配置
	Database    *DatabaseConfig    `json:",omitempty"` // 关系数据配置
	Cachebuf    *CachebufConfig    `json:",omitempty"` // 缓存服务配置
	Debugger    *DebuggerConfig    `json:",omitempty"` // 日志调试配置
	Opentracing *OpentracingConfig `json:",omitempty"` // 链路追踪配置
	Independent interface{}        `json:",omitempty"` // 应用私有配置

	logger *logrus.Entry
	srvdis sd.Clienter
}

// ServicesConfig 基础服务配置，用于设定命名空间、注册的路径、监听的地址等
type ServicesConfig struct {
	RootPath      string `mapstructure:"root-path"`
	Namespace     string `mapstructure:"namespace"`
	ServiceCode   string `mapstructure:"service-code"`
	APIEndpoint   string `mapstructure:"api-endpoint"`
	GRPCAddress   string `mapstructure:"grpc-address"`
	HTTPAddress   string `mapstructure:"http-address"`
	PublicAddress string `mapstructure:"public-address"`
}

// DiscoverConfig 服务注册，服务启动后如何汇报自身
type DiscoverConfig struct {
	Driver    string     `mapstructure:"driver"`
	Endpoints []string   `mapstructure:"endpoints"`
	TLS       *TLSConfig `mapstructure:"tls" json:",omitempty"`
	Heartbeat int64      `mapstructure:"heartbeat"`
}

// SecurityConfig 安全配置，对接认证、鉴权
type SecurityConfig struct {
	Enable bool `mapstructure:"enable"`
}

// DatabaseConfig 数据库设置，指关系数据库，数据不允许丢失，如postgres、mysql
type DatabaseConfig struct {
	Driver         string `mapstructure:"driver"`
	DBName         string `mapstructure:"dbname"`
	User           string `mapstructure:"user"`
	Password       string `mapstructure:"password"`
	Host           string `mapstructure:"host"`
	Port           int    `mapstructure:"port"`
	SSLMode        string `mapstructure:"sslmode"`
	ConnectTimeout int    `mapstructure:"connect-timeout"`
}

// CachebufConfig 缓存配置，区别于数据库配置，缓存的数据可以丢失
type CachebufConfig struct {
	Enable   bool   `mapstructure:"enable"`
	Driver   string `mapstructure:"driver"`
	Address  string `mapstructure:"address"`
	Password string `mapstructure:"password"`
}

// DebuggerConfig 日志配置，用于设定服务启动后日志输出级别格式等
type DebuggerConfig struct {
	LogLevel  string `mapstructure:"log-level"`
	LogFormat string `mapstructure:"log-format"`
}

// OpentracingConfig 分布式链路追踪
type OpentracingConfig struct {
	Enable bool   `mapstructure:"enable"`
	Host   string `mapstructure:"host"`
	Port   int    `mapstructure:"port"`
}

// TLSConfig 证书配置
type TLSConfig struct {
	CAFile             string `mapstructure:"ca-file"`
	CertFile           string `mapstructure:"cert-file"`
	KeyFile            string `mapstructure:"key-file"`
	InsecureSkipVerify bool   `mapstructure:"insecure-skip-verify"`
}

// New 用于初始化获取全局配置实例
func New(v *viper.Viper) (*LocalConfig, error) {
	var lc LocalConfig

	if err := viper.Unmarshal(&lc); err != nil {
		return nil, err
	}

	// 验证几个关键属性是否在
	if lc.Services.RootPath == "" {
		return nil, fmt.Errorf("unknow root-path")
	}
	if lc.Services.Namespace == "" {
		return nil, fmt.Errorf("unknow namespace")
	}
	if lc.Services.ServiceCode == "" {
		return nil, fmt.Errorf("unknow service-code")
	}
	if lc.Services.APIEndpoint == "" {
		return nil, fmt.Errorf("unknow api-endpoint")
	}

	// 初始化默认设置
	if lc.Services.GRPCAddress == "" {
		rand.Seed(time.Now().UnixNano())
		lc.Services.GRPCAddress = fmt.Sprintf("127.0.0.1:%v", 10081+rand.Intn(6000))
	}
	if lc.Services.PublicAddress == "" {
		lc.Services.PublicAddress = lc.Services.GRPCAddress
	}

	return &lc, nil
}

// Init 用于根据配置初始化各个实例
func (c *LocalConfig) Init() error {
	if _, err := c.InitLogger(); err != nil {
		return err
	}

	if _, err := c.InitOpenTracing(); err != nil {
		return err
	}

	return nil
}

// Register 用于登记服务信息至注册中心
func (c *LocalConfig) Register() error {
	sd.Home(c.Services.RootPath, c.Services.Namespace)
	connector, err := sd.NewConnector(c.logger, sd.ETCDV3, strings.Join(c.Discover.Endpoints, ","))
	if err != nil {
		return err
	}

	if c.Discover.TLS != nil {
		tls := &sd.TLSInfo{
			CAFile:   c.Discover.TLS.CAFile,
			CertFile: c.Discover.TLS.CertFile,
			KeyFile:  c.Discover.TLS.KeyFile,
		}
		connector.WithTLSInfo(tls)
	}

	ttl := c.Discover.Heartbeat
	if ttl == 0 {
		ttl = 30
	}

	rawBody, err := json.Marshal(c)
	if err != nil {
		return err
	}

	reg, err := sd.Register(connector, c.GetServiceName(), c.Services.PublicAddress, string(rawBody), ttl)
	if err != nil {
		return fmt.Errorf("Register server err: %v\n", err)
	}

	c.srvdis = reg

	return nil
}

// Deregister 用于撤销注册中心上的服务信息
func (c *LocalConfig) Deregister() error {
	return c.srvdis.Deregister()
}

// GetIndependent 用于获取各个微服务独立的配置
func (c *LocalConfig) GetIndependent(t interface{}) error {
	if c.Independent == nil {
		return fmt.Errorf("independent is nil")
	}

	return mapstructure.Decode(c.Independent, t)
}

// GetServiceName 用于获取微服务名称
func (c *LocalConfig) GetServiceName() string {
	return fmt.Sprintf("%v.%v", c.Services.ServiceCode, c.Services.APIEndpoint)
}
