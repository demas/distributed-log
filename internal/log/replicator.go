package log

import (
	"context"
	"log"
	"sync"

	api "distributed/api/log.v1"

	"google.golang.org/grpc"
)

type Replicator struct {
	LocalServer api.LogClient

	mu      sync.Mutex
	servers map[string]chan struct{}
	closed  bool
	close   chan struct{}
}

func (r *Replicator) Join(name, addr string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.init()
	if r.closed {
		return nil
	}
	if _, ok := r.servers[addr]; ok {
		// already replicating so skip
		return nil
	}
	r.servers[addr] = make(chan struct{})
	go r.replicate(addr, r.servers[addr])
	return nil
}

func (r *Replicator) replicate(addr string, leave chan struct{}) {
	clientOptions := []grpc.DialOption{grpc.WithInsecure()}
	cc, err := grpc.Dial(addr, clientOptions...)
	if err != nil {
		r.err(err)
		return
	}
	defer cc.Close()

	client := api.NewLogClient(cc)

	ctx := context.Background()
	stream, err := client.ConsumeStream(ctx,
		&api.ConsumeRequest{Offset: 0})
	if err != nil {
		r.err(err)
		return
	}
	records := make(chan *api.Record)
	go func() {
		for {
			recv, err := stream.Recv()
			if err != nil {
				r.err(err)
				return
			}
			records <- recv.Record
		}
	}()

	for {
		select {
		case <-r.close:
			return
		case <-leave:
			return
		case record := <-records:
			_, err = r.LocalServer.Produce(ctx,
				&api.ProduceRequest{
					Record: record,
				},
			)
			if err != nil {
				r.err(err)
				return
			}
		}
	}
}

func (r *Replicator) Leave(name, addr string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.init()
	if _, ok := r.servers[addr]; !ok {
		return nil
	}
	close(r.servers[addr])
	delete(r.servers, addr)
	return nil
}

func (r *Replicator) init() {
	if r.servers == nil {
		r.servers = make(map[string]chan struct{})
	}
	if r.close == nil {
		r.close = make(chan struct{})
	}
}

func (r *Replicator) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.init()

	if r.closed {
		return nil
	}
	r.closed = true
	close(r.close)
	return nil
}

func (r *Replicator) err(err error) {
	log.Printf("[ERROR] proglog: %v", err)
}
