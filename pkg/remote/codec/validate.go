package codec

import (
	"context"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"github.com/cloudwego/kitex/pkg/consts"
	"github.com/cloudwego/kitex/pkg/kerrors"
	"github.com/cloudwego/kitex/pkg/remote"
	"github.com/cloudwego/kitex/pkg/remote/codec/perrors"
	"github.com/cloudwego/kitex/pkg/remote/transmeta"
	"github.com/cloudwego/kitex/pkg/rpcinfo"
	"github.com/cloudwego/kitex/pkg/stats"
	"hash/crc32"
	"sync"
)

const (
	PayloadValidatorPrefix   = "PV_"
	maxPayloadChecksumLength = 1024
)

// PayloadValidator is the interface for validating the payload of RPC requests, which allows customized Checksum function.
type PayloadValidator interface {
	// Key returns a key for your validator, which will be the key in ttheader
	Key(ctx context.Context) string

	// Generate generates the checksum of the payload.
	// The value will not be set to the request header if "need" is false.
	// DO NOT modify the input payload since it might be obtained by nocopy API from the underlying buffer.
	Generate(ctx context.Context, outboundPayload []byte) (need bool, checksum string, err error)

	// Validate validates the input payload with the attached checksum.
	// Return pass if validation succeed, or return error.
	Validate(ctx context.Context, expectedValue string, inboundPayload []byte) (pass bool, err error)

	// ProcessBeforeValidate is used to do some preprocess before validate.
	// For example, you can extract some value from ttheader and set to the rpcinfo, which may be useful for validation.
	ProcessBeforeValidate(ctx context.Context, message remote.Message) (context.Context, error)
}

func getValidatorKey(ctx context.Context, p PayloadValidator) string {
	if _, ok := p.(*crcPayloadValidator); ok {
		return p.Key(ctx)
	}
	key := p.Key(ctx)
	return PayloadValidatorPrefix + key
}

func payloadChecksumGenerate(ctx context.Context, pv PayloadValidator, outboundPayload []byte, message remote.Message) (err error) {
	rpcinfo.Record(ctx, message.RPCInfo(), stats.ChecksumGenerateStart, nil)
	defer func() {
		rpcinfo.Record(ctx, message.RPCInfo(), stats.ChecksumGenerateFinish, err)
	}()

	need, value, pErr := pv.Generate(ctx, outboundPayload)
	if pErr != nil {
		err = kerrors.ErrPayloadValidation.WithCause(fmt.Errorf("generate failed, err=%v", pErr))
		return err
	}
	if need {
		if len(value) > maxPayloadChecksumLength {
			err = kerrors.ErrPayloadValidation.WithCause(fmt.Errorf("payload checksum value exceeds the limit, actual length=%d, limit=%d", len(value), maxPayloadChecksumLength))
			return err
		}
		key := getValidatorKey(ctx, pv)
		strInfo := message.TransInfo().TransStrInfo()
		if strInfo != nil {
			strInfo[key] = value
		}
	}
	return nil
}

func payloadChecksumValidate(ctx context.Context, pv PayloadValidator, in remote.ByteBuffer, message remote.Message) (err error) {
	rpcinfo.Record(ctx, message.RPCInfo(), stats.ChecksumValidateStart, nil)
	defer func() {
		rpcinfo.Record(ctx, message.RPCInfo(), stats.ChecksumValidateFinish, err)
	}()

	ctx = fillRPCInfoBeforeValidate(ctx, message)
	// before validate
	ctx, err = pv.ProcessBeforeValidate(ctx, message)
	if err != nil {
		return err
	}

	// get key and value
	key := getValidatorKey(ctx, pv)
	strInfo := message.TransInfo().TransStrInfo()
	if strInfo == nil {
		return nil
	}
	expectedValue := strInfo[key]
	payloadLen := message.PayloadLen() // total length
	payload, err := in.Peek(payloadLen)
	if err != nil {
		return err
	}

	// validate
	pass, err := pv.Validate(ctx, expectedValue, payload)
	if err != nil {
		return kerrors.ErrPayloadValidation.WithCause(fmt.Errorf("validation failed, err=%v", err))
	}
	if !pass {
		return kerrors.ErrPayloadValidation.WithCause(fmt.Errorf("validation failed"))
	}
	return nil
}

// fillRPCInfoBeforeValidate reads header and set into the RPCInfo, which allows Validate() to use RPCInfo.
func fillRPCInfoBeforeValidate(ctx context.Context, message remote.Message) context.Context {
	if message.RPCRole() != remote.Server {
		// only fill when server-side reading the request header
		// TODO: client-side can read from the response header
		return ctx
	}
	ri := message.RPCInfo()
	if ri == nil {
		return ctx
	}
	transInfo := message.TransInfo()
	if transInfo == nil {
		return ctx
	}
	intInfo := transInfo.TransIntInfo()
	if intInfo == nil {
		return ctx
	}
	from := rpcinfo.AsMutableEndpointInfo(ri.From())
	if from != nil {
		if v := intInfo[transmeta.FromService]; v != "" {
			from.SetServiceName(v)
		}
		if v := intInfo[transmeta.FromMethod]; v != "" {
			from.SetMethod(v)
		}
	}
	to := rpcinfo.AsMutableEndpointInfo(ri.To())
	if to != nil {
		// server-side reads "to_method" from ttheader since "method" is set in thrift payload, which has not been unmarshalled
		if v := intInfo[transmeta.ToMethod]; v != "" {
			to.SetMethod(v)
		}
		if v := intInfo[transmeta.ToService]; v != "" {
			to.SetServiceName(v)
		}
	}
	if logid := intInfo[transmeta.LogID]; logid != "" {
		ctx = context.WithValue(ctx, consts.CtxKeyLogID, logid)
	}
	return ctx
}

// NewCRC32PayloadValidator returns a new crcPayloadValidator
func NewCRC32PayloadValidator() PayloadValidator {
	crc32TableOnce.Do(func() {
		crc32cTable = crc32.MakeTable(crc32.Castagnoli)
	})
	return &crcPayloadValidator{}
}

type crcPayloadValidator struct{}

var _ PayloadValidator = &crcPayloadValidator{}

// TODO: 2d slice
func (p *crcPayloadValidator) Key(ctx context.Context) string {
	return transmeta.HeaderCRC32C
}

func (p *crcPayloadValidator) Generate(ctx context.Context, outPayload []byte) (need bool, value string, err error) {
	return true, getCRC32C([][]byte{outPayload}), nil
}

func (p *crcPayloadValidator) Validate(ctx context.Context, expectedValue string, inputPayload []byte) (pass bool, err error) {
	_, realValue, err := p.Generate(ctx, inputPayload)
	if err != nil {
		return false, err
	}
	if realValue != expectedValue {
		return false, perrors.NewProtocolErrorWithType(perrors.InvalidData, expectedValue)
	}
	return true, nil
}

func (p *crcPayloadValidator) ProcessBeforeValidate(ctx context.Context, message remote.Message) (context.Context, error) {
	return ctx, nil
}

// crc32cTable is used for crc32c check
var (
	crc32cTable    *crc32.Table
	crc32TableOnce sync.Once
)

// getCRC32C calculates the crc32c checksum of the input bytes.
// the checksum will be converted into big-endian format and encoded into hex string.
func getCRC32C(payload [][]byte) string {
	if crc32cTable == nil {
		return ""
	}
	csb := make([]byte, Size32)
	var checksum uint32
	for i := 0; i < len(payload); i++ {
		checksum = crc32.Update(checksum, crc32cTable, payload[i])
	}
	binary.BigEndian.PutUint32(csb, checksum)
	return hex.EncodeToString(csb)
}

// flatten2DSlice converts 2d slice to 1d.
// total length should be provided.
func flatten2DSlice(b2 [][]byte, length int) []byte {
	b1 := make([]byte, length)
	off := 0
	for i := 0; i < len(b2); i++ {
		off += copy(b1[off:], b2[i])
	}
	return b1
}
