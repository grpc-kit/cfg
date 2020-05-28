package cfg

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/textproto"
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
	api "github.com/grpc-kit/api/proto/v1"
	"github.com/grpc-kit/pkg/errors"
	"github.com/grpc-kit/pkg/version"
	"github.com/opentracing-contrib/go-stdlib/nethttp"
	opentracing "github.com/opentracing/opentracing-go"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"google.golang.org/grpc"
	"google.golang.org/grpc/balancer/roundrobin"
	"google.golang.org/grpc/metadata"
)

// registerGateway 注册 microservice.pb.gw
func (c *LocalConfig) registerGateway(ctx context.Context,
	gw func(context.Context, *runtime.ServeMux, string, []grpc.DialOption) error,
	opts ...runtime.ServeMuxOption) (*http.ServeMux, error) {

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

			// 忽略对哪些url做追踪
			switch r.URL.Path {
			case "/healthz", "/version":
				// nothing to do
			default:
				if err := span.Tracer().Inject(
					span.Context(),
					opentracing.TextMap,
					opentracing.TextMapCarrier(carrier),
				); err != nil {
					// return metadata.New(carrier)
				}
			}

			// 植入自定义请求头（全局请求ID）
			if val := r.Header.Get(HTTPHeaderRequestID); val != "" {
				carrier[HTTPHeaderRequestID] = val
			} else {
				requestID := "0123456789abcdef0123456789abcdef"

				tch, ok := carrier[TraceContextHeaderName]
				if ok {
					tmps := strings.Split(tch, ":")
					if len(tmps) >= 2 {
						requestID = fmt.Sprintf("%v%v", tmps[0], tmps[1])
					}
				} else {
					requestID = strings.Replace(uuid.New().String(), "-", "", -1)
				}

				carrier[HTTPHeaderRequestID] = requestID
				r.Header.Set(HTTPHeaderRequestID, carrier[HTTPHeaderRequestID])
			}

			return metadata.New(carrier)
		},
	))

	// 统一错误返回结构
	defaultOpts = append(defaultOpts, runtime.WithProtoErrorHandler(
		func(ctx context.Context, mux *runtime.ServeMux, marshaler runtime.Marshaler, w http.ResponseWriter, req *http.Request, err error) {
			s := errors.FromError(err)

			w.Header().Del("Trailer")
			w.Header().Set("Content-Type", marshaler.ContentType())

			requestID := req.Header.Get(HTTPHeaderRequestID)

			t := &api.TracingRequest{}
			if requestID != "" {
				t.Id = requestID
				w.Header().Set(HTTPHeaderRequestID, requestID)
			}

			s = s.AppendDetail(t)

			body := &errors.Response{
				Error: *s,
			}

			buf, err := marshaler.Marshal(body)
			if err != nil {
				s = errors.Internal(ctx, t).WithMessage(err.Error())
				body.Error = *s
				buf, _ = marshaler.Marshal(body)
			}

			md, ok := runtime.ServerMetadataFromContext(ctx)
			if !ok {
			}

			for k := range md.TrailerMD {
				tKey := textproto.CanonicalMIMEHeaderKey(fmt.Sprintf("%s%s", runtime.MetadataTrailerPrefix, k))
				w.Header().Add("Trailer", tKey)
			}

			for k, vs := range md.TrailerMD {
				tKey := fmt.Sprintf("%s%s", runtime.MetadataTrailerPrefix, k)
				for _, v := range vs {
					w.Header().Add(tKey, v)
				}
			}

			w.WriteHeader(s.HTTPStatusCode())

			if _, err := w.Write(buf); err != nil {
			}
		},
	))

	defaultOpts = append(defaultOpts, customOpts...)

	rmux := runtime.NewServeMux(defaultOpts...)

	hmux := http.NewServeMux()
	hmux.Handle("/metrics", promhttp.Handler())
	hmux.Handle("/version", httpHandleGetVersion())
	hmux.Handle("/", nethttp.Middleware(
		opentracing.GlobalTracer(),
		rmux,
		nethttp.OperationNameFunc(func(r *http.Request) string {
			return fmt.Sprintf("http %s %s", strings.ToLower(r.Method), r.URL.Path)
		}),
		nethttp.MWSpanFilter(func(r *http.Request) bool {
			switch r.URL.Path {
			case "/healthz", "/version":
				// 忽略这几个http请求的链路追踪
				return false
			}
			return true
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
		// 忽略HealthCheck请求记录：msg="finished unary call with code OK" grpc.code=OK grpc.method=HealthCheck
		return err == nil && path.Base(fullMethodName) != "HealthCheck"
	})}

	var defaultUnaryOpt []grpc.UnaryServerInterceptor
	defaultUnaryOpt = append(defaultUnaryOpt, grpcprometheus.UnaryServerInterceptor)
	defaultUnaryOpt = append(defaultUnaryOpt, grpcrecovery.UnaryServerInterceptor())
	defaultUnaryOpt = append(defaultUnaryOpt, grpcauth.UnaryServerInterceptor(authValidate(c.Security.Enable)))
	defaultUnaryOpt = append(defaultUnaryOpt, grpcopentracing.UnaryServerInterceptor(tracingFilterFunc))
	defaultUnaryOpt = append(defaultUnaryOpt, grpclogrus.UnaryServerInterceptor(c.logger, logReqFilterOpts...))
	defaultUnaryOpt = append(defaultUnaryOpt, grpclogrus.PayloadUnaryServerInterceptor(c.logger, logPayloadFilterFunc))
	defaultUnaryOpt = append(defaultUnaryOpt, interceptors...)

	return grpc.UnaryInterceptor(grpcmiddleware.ChainUnaryServer(defaultUnaryOpt...))
}

// GetStreamInterceptor xx
func (c *LocalConfig) GetStreamInterceptor(interceptors ...grpc.StreamServerInterceptor) grpc.ServerOption {
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
		// 忽略HealthCheck请求记录：msg="finished unary call with code OK" grpc.code=OK grpc.method=HealthCheck
		return err == nil && path.Base(fullMethodName) != "HealthCheck"
	})}

	var opts []grpc.StreamServerInterceptor
	opts = append(opts, grpcprometheus.StreamServerInterceptor)
	opts = append(opts, grpcrecovery.StreamServerInterceptor())
	opts = append(opts, grpcauth.StreamServerInterceptor(authValidate(c.Security.Enable)))
	opts = append(opts, grpcopentracing.StreamServerInterceptor(tracingFilterFunc))
	opts = append(opts, grpclogrus.StreamServerInterceptor(c.logger, logReqFilterOpts...))
	opts = append(opts, grpclogrus.PayloadStreamServerInterceptor(c.logger, logPayloadFilterFunc))
	opts = append(opts, interceptors...)

	return grpc.StreamInterceptor(grpcmiddleware.ChainStreamServer(opts...))
}

// GetClientDialOption 获取客户端连接的设置
func (c *LocalConfig) GetClientDialOption(customOpts ...grpc.DialOption) []grpc.DialOption {
	var defaultOpts []grpc.DialOption
	defaultOpts = append(defaultOpts, grpc.WithInsecure())
	defaultOpts = append(defaultOpts, grpc.WithBalancerName(roundrobin.Name))
	defaultOpts = append(defaultOpts, customOpts...)
	return defaultOpts
}

// GetClientUnaryInterceptor 获取客户端默认一元拦截器
func (c *LocalConfig) GetClientUnaryInterceptor() []grpc.UnaryClientInterceptor {
	// TODO; 根据fullMethodName进行过滤哪些需要记录payload的，返回false表示不记录
	logPayloadFilterFunc := func(ctx context.Context, fullMethodName string) bool {
		return false
	}

	// TODO; 根据fullMethodName进行过滤哪些需要记录请求状态的，返回false表示不记录
	logReqFilterOpts := []grpclogrus.Option{grpclogrus.WithDecider(func(fullMethodName string, err error) bool {
		// 忽略HealthCheck请求记录：msg="finished unary call with code OK" grpc.code=OK grpc.method=HealthCheck
		return err == nil && path.Base(fullMethodName) != "HealthCheck"
	})}

	var opts []grpc.UnaryClientInterceptor
	opts = append(opts, grpcprometheus.UnaryClientInterceptor)
	opts = append(opts, grpcopentracing.UnaryClientInterceptor())
	opts = append(opts, grpclogrus.UnaryClientInterceptor(c.logger, logReqFilterOpts...))
	opts = append(opts, grpclogrus.PayloadUnaryClientInterceptor(c.logger, logPayloadFilterFunc))
	return opts
}

// GetClientStreamInterceptor 获取客户端默认流拦截器
func (c *LocalConfig) GetClientStreamInterceptor() []grpc.StreamClientInterceptor {
	// TODO; 根据fullMethodName进行过滤哪些需要记录payload的，返回false表示不记录
	logPayloadFilterFunc := func(ctx context.Context, fullMethodName string) bool {
		return false
	}

	// TODO; 根据fullMethodName进行过滤哪些需要记录请求状态的，返回false表示不记录
	logReqFilterOpts := []grpclogrus.Option{grpclogrus.WithDecider(func(fullMethodName string, err error) bool {
		// 忽略HealthCheck请求记录：msg="finished unary call with code OK" grpc.code=OK grpc.method=HealthCheck
		return err == nil && path.Base(fullMethodName) != "HealthCheck"
	})}

	var opts []grpc.StreamClientInterceptor
	opts = append(opts, grpcprometheus.StreamClientInterceptor)
	opts = append(opts, grpcopentracing.StreamClientInterceptor())
	opts = append(opts, grpclogrus.StreamClientInterceptor(c.logger, logReqFilterOpts...))
	opts = append(opts, grpclogrus.PayloadStreamClientInterceptor(c.logger, logPayloadFilterFunc))
	return opts
}

// TODO; 当前未做任何认证
func authValidate(enable bool) grpcauth.AuthFunc {
	return func(ctx context.Context) (context.Context, error) {
		// 如果存在认证请求头，同时帮忙传递下去
		md, ok := metadata.FromIncomingContext(ctx)
		if ok {
			authToken, found := md["authorization"]
			if found {
				for _, token := range authToken {
					ctx = metadata.AppendToOutgoingContext(ctx, "authorization", token)
				}
			}
		}

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

func httpHandleGetVersion() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)

		_, _ = io.WriteString(w, version.Get().String())
	}
}
