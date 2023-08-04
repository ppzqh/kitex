/*
 * Copyright 2023 CloudWeGo Authors
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package generic

import (
	"context"
	"io/ioutil"
	"os"
	"testing"

	"github.com/cloudwego/kitex/internal/mocks"
	"github.com/cloudwego/kitex/internal/test"
	"github.com/cloudwego/kitex/pkg/remote"
	"github.com/cloudwego/kitex/pkg/rpcinfo"
	"github.com/cloudwego/kitex/transport"
)

func TestRun(t *testing.T) {
	t.Run("TestJsonPbCodec", TestJsonPbCodec)
}

func TestJsonPbCodec(t *testing.T) {
	// jpc: json pb codec
	p, err := initPbDescriptorProviderByIDL("./jsonpb_test/idl/echo.proto")
	test.Assert(t, err == nil)
	jpc, err := newJsonPbCodec(p, pbCodec)
	test.Assert(t, err == nil)
	defer jpc.Close()
	test.Assert(t, jpc.Name() == "JSONPb")

	method, err := jpc.getMethod(nil, "Echo")
	test.Assert(t, err == nil)
	test.Assert(t, method.Name == "Echo")

	ctx := context.Background()
	sendMsg := initJsonPbSendMsg(transport.TTHeaderFramed)

	// Marshal side
	out := remote.NewWriterBuffer(256)
	err = jpc.Marshal(ctx, sendMsg, out)

	test.Assert(t, err == nil)

	// UnMarshal side
	recvMsg := initJsonPbRecvMsg()
	buf, err := out.Bytes()
	test.Assert(t, err == nil)
	recvMsg.SetPayloadLen(len(buf))
	in := remote.NewReaderBuffer(buf)
	err = jpc.Unmarshal(ctx, recvMsg, in)
	test.Assert(t, err == nil)
}

func initJsonPbSendMsg(tp transport.Protocol) remote.Message {
	req := &Args{
		Request: `{"message": "send hellos"}`,
		Method:  "Echo",
	}
	svcInfo := mocks.ServiceInfo()
	ink := rpcinfo.NewInvocation("", "Echo")
	ri := rpcinfo.NewRPCInfo(nil, nil, ink, nil, rpcinfo.NewRPCStats())
	msg := remote.NewMessage(req, svcInfo, ri, remote.Call, remote.Client)
	msg.SetProtocolInfo(remote.NewProtocolInfo(tp, svcInfo.PayloadCodec))
	return msg
}

func initJsonPbRecvMsg() remote.Message {
	req := &Args{
		Request: `{"message": "recv hellos"}`,
		Method:  "Echo",
	}
	ink := rpcinfo.NewInvocation("", "Echo")
	ri := rpcinfo.NewRPCInfo(nil, nil, ink, nil, rpcinfo.NewRPCStats())
	msg := remote.NewMessage(req, mocks.ServiceInfo(), ri, remote.Call, remote.Server)
	return msg
}

// Helper Methods
func initPbDescriptorProviderByIDL(pbIdl string) (PbDescriptorProvider, error) {
	pbf, err := os.Open(pbIdl)
	if err != nil {
		return nil, err
	}

	pbContent, err := ioutil.ReadAll(pbf)
	if err != nil {
		return nil, err
	}
	pbf.Close()
	pbp, err := NewPbContentProvider(pbIdl, map[string]string{pbIdl: string(pbContent)})
	if err != nil {
		return nil, err
	}

	return pbp, nil
}
