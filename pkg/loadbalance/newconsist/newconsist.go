package newconsist

import (
	"github.com/bytedance/gopkg/util/xxhash3"
	"github.com/cloudwego/kitex/pkg/discovery"
	"github.com/cloudwego/kitex/pkg/utils"
	"sync"
)

type realNode struct {
	discovery.Instance
}

type virtualNode struct {
	realNode discovery.Instance
	value    uint64
	next     []*virtualNode
}

type ConsistInfoConfig struct {
	VirtualFactor uint32
	Weighted      bool
}

// consistent hash
type ConsistInfo struct {
	mu           sync.RWMutex
	cfg          ConsistInfoConfig
	lastRes      discovery.Result
	virtualNodes *skipList

	cachedByte []byte
}

func NewConsistInfo(result discovery.Result, cfg ConsistInfoConfig) *ConsistInfo {
	info := &ConsistInfo{
		cfg:          cfg,
		virtualNodes: newSkipList(),
		lastRes:      result,
		cachedByte:   make([]byte, 0, utils.GetUIntLen(uint64(10*int(cfg.VirtualFactor)))+maxAddrLength+1),
	}
	for _, ins := range result.Instances {
		info.addAllVirtual(ins)
	}
	return info
}

func (info *ConsistInfo) IsEmpty() bool {
	return len(info.lastRes.Instances) == 0
}

func (info *ConsistInfo) BuildConsistentResult(value uint64) discovery.Instance {
	info.mu.RLock()
	defer info.mu.RUnlock()

	if n := info.virtualNodes.FindGreater(value); n != nil {
		return n.realNode
	}
	return nil
}

func (info *ConsistInfo) Rebalance(change discovery.Change) {
	info.mu.Lock()
	defer info.mu.Unlock()

	info.lastRes = change.Result
	// TODO: optimize update logic
	if len(change.Updated) > 0 {
		info.virtualNodes = newSkipList()
		for _, ins := range change.Result.Instances {
			info.addAllVirtual(ins)
		}
		return
	}
	for _, ins := range change.Added {
		info.addAllVirtual(ins)
	}
	for _, ins := range change.Removed {
		l := ins.Weight() * int(info.cfg.VirtualFactor)
		addrByte := utils.StringToSliceByte(ins.Address().String())
		info.removeAllVirtual(l, info.cachedByte, addrByte)
	}

}

func (info *ConsistInfo) removeAllVirtual(virtualLen int, b []byte, addrByte []byte) {
	for i := 0; i < virtualLen; i++ {
		vv := getVirtualNodeHash(b, addrByte, i)
		info.virtualNodes.Delete(vv)
	}
}

func (info *ConsistInfo) addAllVirtual(node discovery.Instance) {
	l := info.getVirtualNodeLen(node)
	b := info.cachedByte //make([]byte, 0, utils.GetUIntLen(uint64(l))+maxAddrLength+1)
	addrByte := utils.StringToSliceByte(node.Address().String())

	for i := 0; i < l; i++ {
		vv := getVirtualNodeHash(b, addrByte, i)
		info.virtualNodes.Insert(&virtualNode{realNode: node, value: vv})
	}
}

func (info *ConsistInfo) getVirtualNodeLen(node discovery.Instance) int {
	if info.cfg.Weighted {
		return node.Weight() * int(info.cfg.VirtualFactor)
	}
	return int(info.cfg.VirtualFactor)
}

// only for test
func searchRealNode(info *ConsistInfo, node *realNode) (bool, bool) {
	var (
		foundOne = false
		foundAll = true
	)
	l := info.getVirtualNodeLen(node)
	b := make([]byte, 0, utils.GetUIntLen(uint64(l))+maxAddrLength+1)
	addrByte := utils.StringToSliceByte(node.Address().String())

	for i := 0; i < l; i++ {
		vv := getVirtualNodeHash(b, addrByte, i)
		ok := info.virtualNodes.Search(vv)
		if ok {
			foundOne = true
		} else {
			foundAll = false
		}
	}
	return foundOne, foundAll
}

const (
	maxAddrLength int = 45 // used for construct
)

func getVirtualNodeHash(b []byte, addr []byte, idx int) uint64 {
	b = append(b, addr...)
	b = append(b, '#')
	b = append(b, byte(idx))
	hashValue := xxhash3.Hash(b)

	b = b[:0]
	return hashValue
}
