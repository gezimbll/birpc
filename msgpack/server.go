package msgpack

import (
	"io"
	"net/rpc"

	"github.com/cgrates/birpc"
	"github.com/ugorji/go/codec"
)

// MsgpackServerCodec wraps the goRpcCodec and implements the ServerCodec interface
type msgpackServerCodec struct {
	conn  io.ReadWriteCloser
	codec rpc.ServerCodec
}

// NewMsgpackServerCodec creates a new MsgpackServerCodec
func NewMsgpackServerCodec(conn io.ReadWriteCloser) birpc.ServerCodec {
	handle := &codec.MsgpackHandle{}
	return &msgpackServerCodec{
		conn:  conn,
		codec: codec.MsgpackSpecRpc.ServerCodec(conn, handle),
	}
}

// ReadRequestHeader reads the request header
func (c *msgpackServerCodec) ReadRequestHeader(r *birpc.Request) (err error) {
	ugorjiRequest := &rpc.Request{}
	err = c.codec.ReadRequestHeader(ugorjiRequest)
	if err != nil {
		return
	}
	convertFromRPCRequest(ugorjiRequest, r)
	return
}

// ReadRequestBody reads the request body
func (c *msgpackServerCodec) ReadRequestBody(body interface{}) error {
	return c.codec.ReadRequestBody(body)
}

// WriteResponse writes the response
func (c *msgpackServerCodec) WriteResponse(r *birpc.Response, body interface{}) error {
	ugorjiResponse := convertToRPCResponse(r)
	return c.codec.WriteResponse(ugorjiResponse, body)
}

// Close closes the connection
func (c *msgpackServerCodec) Close() error {
	return c.conn.Close()
}

func convertFromRPCRequest(originReq *rpc.Request, req *birpc.Request) {
	req.ServiceMethod = originReq.ServiceMethod
	req.Seq = originReq.Seq
}

func convertToRPCResponse(res *birpc.Response) *rpc.Response {
	return &rpc.Response{
		Seq:           res.Seq,
		ServiceMethod: res.Error,
	}
}
