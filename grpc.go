package cfg

import (
	"context"
	"fmt"
	"io/ioutil"
	"net/http"
	"path"
	"strings"

	"github.com/gogo/gateway"
	"github.com/google/uuid"
	grpcmiddleware "github.com/grpc-ecosystem/go-grpc-middleware"
	grpcauth "github.com/grpc-ecosystem/go-grpc-middleware/auth"
	grpclogrus "github.com/grpc-ecosystem/go-grpc-middleware/logging/logrus"
	grpcrecovery "github.com/grpc-ecosystem/go-grpc-middleware/recovery"
	grpcopentracing "github.com/grpc-ecosystem/go-grpc-middleware/tracing/opentracing"
	grpcprometheus "github.com/grpc-ecosystem/go-grpc-prometheus"
	"github.com/grpc-ecosystem/grpc-gateway/runtime"
	"github.com/opentracing-contrib/go-stdlib/nethttp"
	opentracing "github.com/opentracing/opentracing-go"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

// registerGateway 注册 microservice.pb.gw
func (c *LocalConfig) registerGateway(ctx context.Context,
	gw func(context.Context, *runtime.ServeMux, string, []grpc.DialOption) error,
	opts ...runtime.ServeMuxOption) (http.Handler, error) {

	hmux, rmux := c.getHTTPServeMux(opts...)

	err := gw(ctx,
		rmux,
		fmt.Sprintf("127.0.0.1:%v", c.Services.getGRPCListenPort()),
		c.GetClientDialOption())

	return hmux, err
}

// getHTTPServeMux 获取通用的HTTP路由规则
func (c *LocalConfig) getHTTPServeMux(customOpts ...runtime.ServeMuxOption) (*http.ServeMux, *runtime.ServeMux) {
	// ServeMuxOption如果存在同样的设置选项，则以最后设置为准（见runtime.NewServeMux）
	defaultOpts := make([]runtime.ServeMuxOption, 0)

	// jsonpb使用gogo版本，代替golang/protobuf/jsonpb
	defaultOpts = append(defaultOpts, runtime.WithMarshalerOption(
		runtime.MIMEWildcard, &gateway.JSONPb{OrigName: true, EmitDefaults: true}))

	defaultOpts = append(defaultOpts, runtime.WithMetadata(
		func(ctx context.Context, r *http.Request) metadata.MD {
			span := opentracing.SpanFromContext(ctx)

			carrier := make(map[string]string)
			// 植入自定义请求头（全局请求ID）
			if val := r.Header.Get("x-tr-request-id"); val != "" {
				carrier["x-tr-request-id"] = val
			} else {
				carrier["x-tr-request-id"] = strings.Replace(uuid.New().String(), "-", "", -1)
			}

			// 忽略对哪些url做追踪
			switch r.URL.Path {
			case "healthz", "version":
			default:
				if err := span.Tracer().Inject(
					span.Context(),
					opentracing.TextMap,
					opentracing.TextMapCarrier(carrier),
				); err != nil {
					return metadata.New(carrier)
				}
			}

			return metadata.New(carrier)
		},
	))

	defaultOpts = append(defaultOpts, customOpts...)

	rmux := runtime.NewServeMux(defaultOpts...)

	hmux := http.NewServeMux()
	hmux.Handle("/metrics", promhttp.Handler())
	hmux.Handle("/swagger.json", httpSwaggerFromFile("/opt/swagger.json"))
	hmux.Handle("/", nethttp.Middleware(
		opentracing.GlobalTracer(),
		rmux,
		nethttp.OperationNameFunc(func(r *http.Request) string {
			return fmt.Sprintf("http %s %s", strings.ToLower(r.Method), r.URL.Path)
		}),
	))

	return hmux, rmux
}

// GetUnaryInterceptor 用于获取gRPC的一元拦截器
func (c *LocalConfig) GetUnaryInterceptor(interceptors ...grpc.UnaryServerInterceptor) grpc.ServerOption {
	// TODO; 根据fullMethodName进行过滤哪些需要记录gRPC调用链，返回false表示不记录
	tracingFilterFunc := grpcopentracing.WithFilterFunc(func(ctx context.Context, fullMethodName string) bool {
		return path.Base(fullMethodName) != "HealthCheck"
	})

	// TODO; 根据fullMethodName进行过滤哪些需要记录payload的，返回false表示不记录
	logPayloadFilterFunc := func(ctx context.Context, fullMethodName string, servingObject interface{}) bool {
		return false
	}

	// TODO; 根据fullMethodName进行过滤哪些需要记录请求状态的，返回false表示不记录
	logReqFilterOpts := []grpclogrus.Option{grpclogrus.WithDecider(func(fullMethodName string, err error) bool {
		return err == nil && path.Base(fullMethodName) == "HealthCheck"
	})}

	defaultUnaryOpt := make([]grpc.UnaryServerInterceptor, 0)
	defaultUnaryOpt = append(defaultUnaryOpt, grpcprometheus.UnaryServerInterceptor)
	defaultUnaryOpt = append(defaultUnaryOpt, grpcrecovery.UnaryServerInterceptor())
	defaultUnaryOpt = append(defaultUnaryOpt, grpcauth.UnaryServerInterceptor(authValidate(c.Security.Enable)))
	defaultUnaryOpt = append(defaultUnaryOpt, grpcopentracing.UnaryServerInterceptor(tracingFilterFunc))
	defaultUnaryOpt = append(defaultUnaryOpt, grpclogrus.UnaryServerInterceptor(c.logger, logReqFilterOpts...))
	defaultUnaryOpt = append(defaultUnaryOpt, grpclogrus.PayloadUnaryServerInterceptor(c.logger, logPayloadFilterFunc))
	defaultUnaryOpt = append(defaultUnaryOpt, interceptors...)

	return grpc.UnaryInterceptor(grpcmiddleware.ChainUnaryServer(defaultUnaryOpt...))
}

// GetClientDialOption 获取客户端连接的设置
func (c *LocalConfig) GetClientDialOption(customOpts ...grpc.DialOption) []grpc.DialOption {
	defaultOpts := make([]grpc.DialOption, 0)
	defaultOpts = append(defaultOpts, grpc.WithInsecure())
	defaultOpts = append(defaultOpts, customOpts...)
	return defaultOpts
}

// TODO; 当前未做任何认证
func authValidate(enable bool) grpcauth.AuthFunc {
	return func(ctx context.Context) (context.Context, error) {
		if !enable {
			return ctx, nil
		}

		/*
		   bearerToken, err := grpcauth.AuthFromMD(ctx, "bearer")
		   fmt.Println("bearer:", bearerToken, "err:", err)

		   baseToken, err := grpcauth.AuthFromMD(ctx, "basic")
		   fmt.Println("base auth:", baseToken, "err:", err)
		*/

		return ctx, nil
	}
}

// TODO; 待改造转为静态文件
func httpSwaggerFromFile(swaggerFile string) http.HandlerFunc {
	notFoundHandler := func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		if _, err := w.Write([]byte("not found swagger")); err != nil {
			return
		}
	}

	swaggerJSONBody, err := ioutil.ReadFile(swaggerFile)
	if err != nil {
		return notFoundHandler
	}

	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Access-Control-Allow-Origin", "*")

		w.WriteHeader(http.StatusOK)
		if _, err := w.Write(swaggerJSONBody); err != nil {
			fmt.Println("write err:", err)
		}
	}
}
