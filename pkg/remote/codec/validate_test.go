package codec

import (
	"context"
	"errors"
	"github.com/bytedance/gopkg/util/xxhash3"
	"github.com/cloudwego/kitex/internal/test"
	"github.com/cloudwego/kitex/pkg/kerrors"
	"github.com/cloudwego/kitex/pkg/remote"
	"github.com/cloudwego/kitex/transport"
	"strconv"
	"testing"
)

var _ PayloadValidator = &mockPayloadValidator{}

type mockPayloadValidator struct {
}

const (
	mockGenerateErrorKey    = "mockGenerateError"
	mockExceedLimitKey      = "mockExceedLimit"
	mockPreprocessKey       = "preprocessSucceedKey"
	mockPreprocessFailedKey = "preprocessFailed"
)

type preprocessStruct struct {
	value string
}

func (m *mockPayloadValidator) Key(ctx context.Context) string {
	return "mockValidator"
}

func (m *mockPayloadValidator) Generate(ctx context.Context, outPayload []byte) (need bool, value string, err error) {
	if l := ctx.Value(mockExceedLimitKey); l != nil {
		// return value with length exceeding the limit
		return true, string(make([]byte, maxPayloadChecksumLength+1)), nil
	}
	if l := ctx.Value(mockGenerateErrorKey); l != nil {
		return false, "", errors.New("mockGenerateError")
	}
	hash := xxhash3.Hash(outPayload)
	return true, strconv.FormatInt(int64(hash), 10), nil
}

func (m *mockPayloadValidator) Validate(ctx context.Context, expectedValue string, inputPayload []byte) (pass bool, err error) {
	_, value, err := m.Generate(ctx, inputPayload)
	if err != nil {
		return false, err
	}
	return value == expectedValue, nil
}

func (m *mockPayloadValidator) ProcessBeforeValidate(ctx context.Context, message remote.Message) (context.Context, error) {
	if l := ctx.Value(mockPreprocessFailedKey); l != nil {
		return ctx, errors.New("mockPreprocessFailed")
	}
	s := ctx.Value(mockPreprocessKey)
	if s != nil {
		s.(*preprocessStruct).value = "123"
	}
	return ctx, nil
}

func TestPayloadValidator(t *testing.T) {
	p := &mockPayloadValidator{}
	payload := make([]byte, 0)

	need, value, err := p.Generate(context.Background(), payload)
	test.Assert(t, err == nil, err)
	test.Assert(t, need)

	ctx := context.Background()
	s := &preprocessStruct{value: "1"}
	ctx = context.WithValue(ctx, mockPreprocessKey, s)
	ctx, err = p.ProcessBeforeValidate(ctx, nil)
	test.Assert(t, err == nil, err)
	test.Assert(t, s.value == "123")

	pass, err := p.Validate(ctx, value, payload)
	test.Assert(t, err == nil, err)
	test.Assert(t, pass, true)
}

func TestPayloadChecksumGenerate(t *testing.T) {
	payload := make([]byte, 1024)
	for i := 0; i < len(payload); i++ {
		payload[i] = byte(i)
	}
	pv := &mockPayloadValidator{}

	// success
	message := initClientSendMsg(transport.TTHeader)
	strInfo := message.TransInfo().TransStrInfo()
	ctx := context.Background()
	err := payloadChecksumGenerate(ctx, pv, payload, message)
	test.Assert(t, err == nil, err)
	test.Assert(t, len(strInfo) != 0)
	test.Assert(t, strInfo[getValidatorKey(ctx, pv)] != "")

	// failed, generate error
	message = initClientSendMsg(transport.TTHeader)
	strInfo = message.TransInfo().TransStrInfo()
	ctx = context.WithValue(context.Background(), mockGenerateErrorKey, "true")
	err = payloadChecksumGenerate(ctx, pv, payload, message)
	test.Assert(t, err != nil, err)
	test.Assert(t, errors.Is(err, kerrors.ErrPayloadValidation))

	// failed, exceed limit
	message = initClientSendMsg(transport.TTHeader)
	strInfo = message.TransInfo().TransStrInfo()
	ctx = context.WithValue(context.Background(), mockExceedLimitKey, "true")
	err = payloadChecksumGenerate(ctx, pv, payload, message)
	test.Assert(t, err != nil, err)
	test.Assert(t, errors.Is(err, kerrors.ErrPayloadValidation))
}

func TestPayloadChecksumValidate(t *testing.T) {
	// prepare
	payload := make([]byte, 1024)
	for i := 0; i < len(payload); i++ {
		payload[i] = byte(i)
	}
	pv := &mockPayloadValidator{}
	sendMsg := initClientSendMsg(transport.TTHeader)
	ctx := context.Background()
	err := payloadChecksumGenerate(ctx, pv, payload, sendMsg)
	test.Assert(t, err == nil, err)

	// success
	in := remote.NewReaderBuffer(payload)
	message := initClientRecvMsg()
	message.TransInfo().PutTransStrInfo(sendMsg.TransInfo().TransStrInfo()) // put header strinfo
	message.SetPayloadLen(len(payload))
	s := &preprocessStruct{value: "1"}
	ctx = context.WithValue(context.Background(), mockPreprocessKey, s)
	err = payloadChecksumValidate(ctx, pv, in, message)
	test.Assert(t, err == nil, err)
	test.Assert(t, s.value == "123")

	// validate failed, checksum validation error
	in = remote.NewReaderBuffer(payload)
	message = initClientRecvMsg()
	// don't put header strinfo
	message.SetPayloadLen(len(payload))
	err = payloadChecksumValidate(context.Background(), pv, in, message)
	test.Assert(t, err != nil)

	// validate failed, preprocess error
	ctx = context.WithValue(context.Background(), mockPreprocessFailedKey, true)
	s = &preprocessStruct{value: "1"}
	ctx = context.WithValue(ctx, mockPreprocessKey, s)
	err = payloadChecksumValidate(ctx, pv, in, message)
	test.Assert(t, err != nil)
	test.Assert(t, s.value == "1") // will not be modified
}
