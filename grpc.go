package cfg

import (
	"context"
	"fmt"
	"io/ioutil"
	"net/http"
	"path"
	"strings"

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

// GetHTTPServeMux 获取通用的HTTP路由规则
func (c *LocalConfig) GetHTTPServeMux(opts ...runtime.ServeMuxOption) (*http.ServeMux, *runtime.ServeMux) {
	defaultMarshalerOpt := runtime.WithMarshalerOption(runtime.MIMEWildcard, &runtime.JSONPb{OrigName: true, EmitDefaults: true})
	defaultMetadataOpt := runtime.WithMetadata(
		func(ctx context.Context, r *http.Request) metadata.MD {
			span := opentracing.SpanFromContext(ctx)
			carrier := make(map[string]string)

			if err := span.Tracer().Inject(
				span.Context(),
				opentracing.TextMap,
				opentracing.TextMapCarrier(carrier),
			); err != nil {
				fmt.Println("err:", err)
			}
			return metadata.New(carrier)
		},
	)

	opts = append(opts, defaultMarshalerOpt)
	opts = append(opts, defaultMetadataOpt)

	rmux := runtime.NewServeMux(opts...)

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
func (c *LocalConfig) GetUnaryInterceptor() grpc.ServerOption {
	// TODO; 如果返回false，则不对该方法进行链路追踪（在gRPC调用层面）
	tracingFilterFunc := grpcopentracing.WithFilterFunc(func(ctx context.Context, fullMethodName string) bool {
		return path.Base(fullMethodName) == "HealthCheck"
	})

	// TODO; 根据fullMethodName进行过滤哪些需要记录payload的，返回false表示不记录
	logPayloadFilterFunc := func(ctx context.Context, fullMethodName string, servingObject interface{}) bool {
		return false
	}

	// TODO; 根据fullMethodName进行过滤哪些需要记录请求状态的，返回false表示不记录
	logReqFilterOpts := []grpclogrus.Option{grpclogrus.WithDecider(func(fullMethodName string, err error) bool {
		return err == nil && path.Base(fullMethodName) == "HealthCheck"
	})}

	s := grpc.UnaryInterceptor(grpcmiddleware.ChainUnaryServer(
		grpcprometheus.UnaryServerInterceptor,
		grpcrecovery.UnaryServerInterceptor(),
		grpcauth.UnaryServerInterceptor(authValidate(c.Security.Enable)),
		grpcopentracing.UnaryServerInterceptor(tracingFilterFunc),
		// 记录gRPC的请求状态
		grpclogrus.UnaryServerInterceptor(c.logger, logReqFilterOpts...),
		// 记录gRPC的请求内容；logpayload必须在grpclogrus.UnaryServerInterceptor之后
		grpclogrus.PayloadUnaryServerInterceptor(c.logger, logPayloadFilterFunc)))

	return s
}

// GetClientDialOption 获取客户端连接的设置
func (c *LocalConfig) GetClientDialOption() []grpc.DialOption {
	return []grpc.DialOption{grpc.WithInsecure()}
}

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
