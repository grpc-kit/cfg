package cfg

import (
	"fmt"
	"strconv"
	"strings"
)

// GetGRPCListenHostPort 解析gRPC监听的IP地址与端口
func (s ServicesConfig) GetGRPCListenHostPort() (string, int, error) {
	temps := strings.Split(s.GRPCAddress, ":")
	if len(temps) != 2 {
		return "", -1, fmt.Errorf("grpc-address format invalid")
	}

	port, err := strconv.Atoi(temps[1])
	if err != nil {
		return "", -1, fmt.Errorf("grpc-address format invalid")
	}

	return temps[0], port, nil
}

// GetGRPCListenPort 本地配置中gRPC监听的端口
func (s ServicesConfig) GetGRPCListenPort() int {
	_, port, _ := s.GetGRPCListenHostPort()
	return port
}
