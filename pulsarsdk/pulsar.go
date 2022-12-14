package pulsarsdk

import (
	"context"
	"github.com/apache/pulsar-client-go/pulsar"
	"github.com/google/uuid"
	"github.com/panjf2000/ants/v2"
	"time"
)

var (
	cli              *Client
	done             = make(chan struct{}, 1)
	subscriptMsgPool *ants.Pool
)

type Client struct {
	pulsar.Client
	prodList []*Producer
	subList  []*Consumer
}

func GetClient() *Client {
	return cli
}

type SubscribeCallBack func(message pulsar.Message, err error)
type ProductCallBack func(id pulsar.MessageID, message *pulsar.ProducerMessage, callBackErr error)

func Init(option pulsar.ClientOptions, subscribePoolSize int) (err error) {
	client, err := pulsar.NewClient(option)
	if err != nil {
		return
	}
	cli = new(Client)
	cli.Client = client
	cli.subList = make([]*Consumer, 0, 10)
	cli.prodList = make([]*Producer, 0, 10)
	if subscribePoolSize < 0 {
		subscribePoolSize = 1024
	}

	pool, err := ants.NewPool(subscribePoolSize, ants.WithNonblocking(true), ants.WithPreAlloc(true))
	if err != nil {
		return err
	}
	subscriptMsgPool = pool
	return
}

type Producer struct {
	prod pulsar.Producer
}

type Consumer struct {
	consumer pulsar.Consumer
}

func NewProducer(topic string, sendTimeout int) (prod *Producer, err error) {
	srcProd, err := cli.CreateProducer(pulsar.ProducerOptions{
		Topic:               topic,
		SendTimeout:         time.Second * time.Duration(sendTimeout),
		MaxPendingMessages:  1000000,
		DisableBatching:     false,
		BatchingMaxMessages: 2000,
		BatchingMaxSize:     1024 * 1024,
	})
	if err != nil {
		return
	}
	prod = new(Producer)
	prod.prod = srcProd
	cli.prodList = append(cli.prodList, prod)
	return
}

func NewConsumer(topic string) (con *Consumer, err error) {
	srcCon, err := cli.Subscribe(pulsar.ConsumerOptions{
		Topic:                       topic,
		Type:                        pulsar.Shared,
		SubscriptionName:            topic + uuid.NewString(),
		RetryEnable:                 true,
		AutoDiscoveryPeriod:         time.Duration(time.Now().Unix()),
		SubscriptionInitialPosition: pulsar.SubscriptionPositionEarliest,
		ReceiverQueueSize:           2000,
	})
	if err != nil {
		return
	}
	con = new(Consumer)
	con.consumer = srcCon
	cli.subList = append(cli.subList, con)
	return
}

func GetSrcConsumer(con *Consumer) pulsar.Consumer {
	return con.consumer
}

// ????????????
func (p *Producer) ProductMsg(ctx context.Context, msg []byte, callBack ProductCallBack) {
	p.prod.SendAsync(ctx, &pulsar.ProducerMessage{
		Payload: msg,
	}, func(id pulsar.MessageID, message *pulsar.ProducerMessage, callBackErr error) {
		callBack(id, message, callBackErr)
	})
	return
}

func SubscribeMsg(ctx context.Context, topic string, callBack SubscribeCallBack) {
	con, err := NewConsumer(topic)
	if err != nil {
		callBack(nil, err)
		return
	}
	srcCon := con.consumer
	for {
		select {
		case msg, ok := <-srcCon.Chan():
			if !ok {
				continue
			}
			err = subscriptMsgPool.Submit(func() {
				callBack(msg, nil)
				msg.AckID(msg.ID())
			})
			if err != nil {
				callBack(nil, err)
			}
		case <-ctx.Done():
			break
		case <-done:
			break
		}
	}
}

func Closed() {
	for i := 0; i < len(cli.subList); i++ {
		sub := *cli.subList[i]
		con := sub.consumer
		con.Close()
	}
	for i := 0; i < len(cli.prodList); i++ {
		prod := cli.prodList[i].prod
		_ = prod.Flush()
		prod.Close()
	}
	done <- struct{}{}
	cli.Close()
}
