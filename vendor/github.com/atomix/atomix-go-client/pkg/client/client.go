package client

import (
	"context"
	"errors"
	"fmt"
	"github.com/atomix/atomix-go-client/pkg/client/_map"
	"github.com/atomix/atomix-go-client/pkg/client/counter"
	"github.com/atomix/atomix-go-client/pkg/client/election"
	"github.com/atomix/atomix-go-client/pkg/client/lock"
	"github.com/atomix/atomix-go-client/pkg/client/primitive"
	"github.com/atomix/atomix-go-client/pkg/client/protocol"
	"github.com/atomix/atomix-go-client/pkg/client/session"
	"github.com/atomix/atomix-go-client/pkg/client/set"
	"github.com/atomix/atomix-go-client/proto/atomix/controller"
	"github.com/atomix/atomix-go-client/proto/atomix/partition"
	"google.golang.org/grpc"
	"net"
)

// NewClient returns a new Atomix client
func NewClient(address string, opts ...ClientOption) (*Client, error) {
	options := applyOptions(opts...)

	// Set up a connection to the server.
	conn, err := grpc.Dial(address, grpc.WithInsecure())
	if err != nil {
		return nil, err
	}

	return &Client{
		conn:        conn,
		application: options.application,
		namespace:   options.namespace,
		conns:       []*grpc.ClientConn{},
	}, nil
}

// Atomix primitive client
type Client struct {
	application string
	namespace   string
	conn        *grpc.ClientConn
	conns       []*grpc.ClientConn
}

// CreateGroup creates a new partition group
func (c *Client) CreateGroup(ctx context.Context, name string, partitions int, partitionSize int, protocol protocol.Protocol) (*PartitionGroup, error) {
	client := controller.NewControllerServiceClient(c.conn)
	request := &controller.CreatePartitionGroupRequest{
		Id: &partition.PartitionGroupId{
			Name:      name,
			Namespace: c.namespace,
		},
		Spec: &partition.PartitionGroupSpec{
			Partitions:    uint32(partitions),
			PartitionSize: uint32(partitionSize),
			Group:         protocol.Spec().GetGroup(),
		},
	}

	_, err := client.CreatePartitionGroup(ctx, request)
	if err != nil {
		return nil, err
	}
	return c.GetGroup(ctx, name)
}

// GetGroups returns a list of all partition group in the client's namespace
func (c *Client) GetGroups(ctx context.Context) ([]*PartitionGroup, error) {
	client := controller.NewControllerServiceClient(c.conn)
	request := &controller.GetPartitionGroupsRequest{
		Id: &partition.PartitionGroupId{
			Namespace: c.namespace,
		},
	}

	response, err := client.GetPartitionGroups(ctx, request)
	if err != nil {
		return nil, err
	}

	groups := make([]*PartitionGroup, len(response.Groups))
	for i, groupProto := range response.Groups {
		group, err := c.newGroup(groupProto)
		if err != nil {
			return nil, err
		}
		groups[i] = group
	}
	return groups, nil
}

// GetGroup returns a partition group primitive client
func (c *Client) GetGroup(ctx context.Context, name string) (*PartitionGroup, error) {
	client := controller.NewControllerServiceClient(c.conn)
	request := &controller.GetPartitionGroupsRequest{
		Id: &partition.PartitionGroupId{
			Name:      name,
			Namespace: c.namespace,
		},
	}

	response, err := client.GetPartitionGroups(ctx, request)
	if err != nil {
		return nil, err
	}

	if len(response.Groups) == 0 {
		return nil, errors.New("unknown partition group " + name)
	} else if len(response.Groups) > 1 {
		return nil, errors.New("partition group " + name + " is ambiguous")
	}
	return c.newGroup(response.Groups[0])
}

func (c *Client) newGroup(groupProto *partition.PartitionGroup) (*PartitionGroup, error) {
	partitions := make([]*grpc.ClientConn, len(groupProto.Partitions))
	for i, partitionProto := range groupProto.Partitions {
		ep := partitionProto.Endpoints[0]
		conn, err := grpc.Dial(fmt.Sprintf("%s:%d", ep.Host, ep.Port), grpc.WithInsecure())
		if err != nil {
			return nil, err
		}
		partitions[i] = conn
		c.conns = append(c.conns, conn)
	}

	proto := ""
	switch groupProto.Spec.Group.(type) {
	case *partition.PartitionGroupSpec_Raft:
		proto = "raft"
	case *partition.PartitionGroupSpec_PrimaryBackup:
		proto = "backup"
	case *partition.PartitionGroupSpec_Log:
		proto = "log"
	}

	return &PartitionGroup{
		Namespace:     groupProto.Id.Namespace,
		Name:          groupProto.Id.Name,
		Partitions:    int(groupProto.Spec.Partitions),
		PartitionSize: int(groupProto.Spec.PartitionSize),
		Protocol:      proto,
		application:   c.application,
		partitions:    partitions,
	}, nil
}

// DeleteGroup deletes a partition group via the controller
func (c *Client) DeleteGroup(ctx context.Context, name string) error {
	client := controller.NewControllerServiceClient(c.conn)
	request := &controller.DeletePartitionGroupRequest{
		Id: &partition.PartitionGroupId{
			Name:      name,
			Namespace: c.namespace,
		},
	}
	_, err := client.DeletePartitionGroup(ctx, request)
	return err
}

// Close closes the client
func (c *Client) Close() error {
	var result error
	for _, conn := range c.conns {
		err := conn.Close()
		if err != nil {
			result = err
		}
	}

	if err := c.conn.Close(); err != nil {
		return err
	}
	return result
}

// NewGroup returns a partition group client
func NewGroup(address string, opts ...ClientOption) (*PartitionGroup, error) {
	_, records, err := net.LookupSRV("", "", address)
	if err != nil {
		return nil, err
	}

	options := applyOptions(opts...)
	partitions := make([]*grpc.ClientConn, len(records))
	for i, record := range records {
		conn, err := grpc.Dial(fmt.Sprintf("%s:%d", record.Target, record.Port), grpc.WithInsecure())
		if err != nil {
			return nil, err
		}
		partitions[i] = conn
	}
	return &PartitionGroup{
		Namespace:   options.namespace,
		Name:        address,
		application: options.application,
		partitions:  partitions,
	}, nil
}

// Primitive partition group.
type PartitionGroup struct {
	counter.CounterClient
	_map.MapClient
	election.ElectionClient
	lock.LockClient
	set.SetClient

	Namespace     string
	Name          string
	Partitions    int
	PartitionSize int
	Protocol      string

	application string
	partitions  []*grpc.ClientConn
}

func (g *PartitionGroup) GetCounter(ctx context.Context, name string, opts ...session.SessionOption) (counter.Counter, error) {
	return counter.New(ctx, primitive.NewName(g.Namespace, g.Name, g.application, name), g.partitions, opts...)
}

func (g *PartitionGroup) GetElection(ctx context.Context, name string, opts ...session.SessionOption) (election.Election, error) {
	return election.New(ctx, primitive.NewName(g.Namespace, g.Name, g.application, name), g.partitions, opts...)
}

func (g *PartitionGroup) GetLock(ctx context.Context, name string, opts ...session.SessionOption) (lock.Lock, error) {
	return lock.New(ctx, primitive.NewName(g.Namespace, g.Name, g.application, name), g.partitions, opts...)
}

func (g *PartitionGroup) GetMap(ctx context.Context, name string, opts ...session.SessionOption) (_map.Map, error) {
	return _map.New(ctx, primitive.NewName(g.Namespace, g.Name, g.application, name), g.partitions, opts...)
}

func (g *PartitionGroup) GetSet(ctx context.Context, name string, opts ...session.SessionOption) (set.Set, error) {
	return set.New(ctx, primitive.NewName(g.Namespace, g.Name, g.application, name), g.partitions, opts...)
}