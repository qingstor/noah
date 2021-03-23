package task

import (
	"context"
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/aos-dev/go-toolbox/natszap"
	"github.com/aos-dev/go-toolbox/zapcontext"
	"github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nats.go"
	natsproto "github.com/nats-io/nats.go/encoders/protobuf"
	"go.uber.org/zap"
	"google.golang.org/grpc"

	"github.com/aos-dev/noah/proto"
)

type Portal struct {
	queue       *nats.EncodedConn
	nodes       []string
	nodeAddrMap map[string]string

	config PortalConfig

	proto.UnimplementedNodeServer
}

type PortalConfig struct {
	Host      string
	GrpcPort  int
	QueuePort int
}

func (p PortalConfig) GrpcAddr() string {
	return fmt.Sprintf("%s:%d", p.Host, p.GrpcPort)
}

func (p PortalConfig) QueueAddr() string {
	return fmt.Sprintf("%s:%d", p.Host, p.QueuePort)
}

func NewPortal(ctx context.Context, cfg PortalConfig) (p *Portal, err error) {
	logger := zapcontext.From(ctx)

	p = &Portal{
		nodeAddrMap: map[string]string{},
		config:      cfg,
	}

	// Setup grpc server.
	grpcSrv := grpc.NewServer()
	proto.RegisterNodeServer(grpcSrv, p)
	go func() {
		l, err := net.Listen("tcp", cfg.GrpcAddr())
		if err != nil {
			return
		}
		err = grpcSrv.Serve(l)
		if err != nil {
			return
		}
	}()

	// Setup queue server.
	srv, err := server.NewServer(&server.Options{
		Host:  cfg.Host,
		Port:  cfg.QueuePort,
		Debug: true, // FIXME: allow used for developing
	})
	if err != nil {
		return
	}

	go func() {
		srv.SetLoggerV2(natszap.NewLog(logger), false, false, false)

		err = server.Run(srv)
		if err != nil {
			logger.Error("server run", zap.Error(err))
		}
	}()

	if !srv.ReadyForConnections(time.Second) {
		panic(fmt.Errorf("server start too slow"))
	}

	conn, err := nats.Connect(srv.Addr().String())
	if err != nil {
		return
	}
	queue, err := nats.NewEncodedConn(conn, natsproto.PROTOBUF_ENCODER)
	if err != nil {
		return
	}
	p.queue = queue

	return p, nil
}

func (p *Portal) Register(ctx context.Context, request *proto.RegisterRequest) (*proto.RegisterReply, error) {
	logger := zapcontext.From(ctx)

	logger.Info("receive register request",
		zap.String("id", request.Id),
		zap.String("addr", request.Addr))
	p.nodes = append(p.nodes, request.Id)
	p.nodeAddrMap[request.Id] = request.Addr

	return &proto.RegisterReply{
		Addr:    p.config.QueueAddr(),
		Subject: "tasks",
	}, nil
}

func (p *Portal) Upgrade(ctx context.Context, request *proto.UpgradeRequest) (*proto.UpgradeReply, error) {
	logger := zapcontext.From(ctx)

	logger.Info("node addr map", zap.Reflect("map", p.nodeAddrMap))
	return &proto.UpgradeReply{
		NodeId:  p.nodes[0],
		Addr:    p.nodeAddrMap[p.nodes[0]],
		Subject: fmt.Sprintf("task.%s", request.TaskId),
	}, nil
}

// Publish will publish a task on "tasks" queue.
func (p *Portal) Publish(ctx context.Context, task *proto.Task) (err error) {
	_ = zapcontext.From(ctx)

	// TODO: We need to maintain all tasks in db maybe.
	err = p.queue.PublishRequest("tasks", task.Id, task)
	if err != nil {
		return
	}
	return
}

// Wait will wait for all nodes' replies on specific task.
func (p *Portal) Wait(ctx context.Context, task *proto.Task) (err error) {
	logger := zapcontext.From(ctx)

	wg := &sync.WaitGroup{}
	wg.Add(len(p.nodes))
	sub, err := p.queue.Subscribe(task.Id, func(tr *proto.TaskReply) {
		defer wg.Done()

		switch tr.Status {
		case JobStatusSucceed:
			logger.Info("task succeed", zap.String("id", tr.Id))
		default:
			logger.Error("task failed", zap.String("id", tr.Id),
				zap.String("error", tr.Message),
			)
		}
	})
	if err != nil {
		return err
	}
	defer sub.Unsubscribe()

	wg.Wait()
	return
}

// Drain means portal close the queue and no new task will be published.
func (p *Portal) Drain(ctx context.Context) (err error) {
	return p.queue.Drain()
}
