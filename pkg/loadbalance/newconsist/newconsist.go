package newconsist

import (
	"github.com/bytedance/gopkg/util/xxhash3"
	"github.com/cloudwego/kitex/pkg/discovery"
	"github.com/cloudwego/kitex/pkg/utils"
	"math/rand"
	"sync"
)

func getValueOfInstance(ins discovery.Instance) string {
	return ins.Address().String()
}

func getHashOfInstance(ins discovery.Instance) uint64 {
	return xxhash3.HashString(ins.Address().String())
}

const (
	MAX_LEVEL = 10 // 定义跳表的最大层数
)

type instanceNode struct {
	discovery.Instance
}

// 跳表节点结构
type virtualNode struct {
	realNode *instanceNode
	value    uint64
	next     []*virtualNode
}

func newNode(origin *instanceNode, value uint64, level int) *virtualNode {
	return &virtualNode{
		realNode: origin,
		value:    value,
		next:     make([]*virtualNode, level),
	}
}

// 跳表结构
type skipList struct {
	dummy      *virtualNode
	totalLevel int

	cached []*virtualNode
}

// 创建一个新的跳表
func newSkipList() *skipList {
	return &skipList{
		dummy:      newNode(nil, 0, MAX_LEVEL),
		totalLevel: 1,
		cached:     make([]*virtualNode, MAX_LEVEL),
	}
}

// 随机生成节点的层数
func randomLevel() int {
	level := 1
	for rand.Float32() < 0.5 && level < MAX_LEVEL {
		level++
	}
	return level
}

func maxValue(a, b int) int {
	if a > b {
		return a
	} else {
		return b
	}
}

// 插入值到跳表
func (sl *skipList) Insert(n *virtualNode) {
	level := randomLevel()
	update := sl.cached[:maxValue(level, sl.totalLevel)]
	if level > sl.totalLevel {
		for i := sl.totalLevel; i < level; i++ {
			update[i-1] = sl.dummy
		}
		sl.totalLevel = level
	}

	current := sl.dummy
	for i := sl.totalLevel - 1; i >= 0; i-- {
		for len(current.next) > i && current.next[i] != nil && current.next[i].value < n.value {
			current = current.next[i]
		}
		update[i] = current
	}

	newNode := n
	n.next = make([]*virtualNode, level)
	for i := 0; i < level; i++ {
		newNode.next[i] = update[i].next[i]
		update[i].next[i] = newNode
	}
}

// 查找值在跳表中是否存在
func (sl *skipList) Search(value uint64) bool {
	current := sl.dummy
	for i := sl.totalLevel - 1; i >= 0; i-- {
		for current.next[i] != nil && current.next[i].value < value {
			current = current.next[i]
		}
	}
	current = current.next[0]
	return current != nil && current.value == value
}

func (sl *skipList) FindGreater(value uint64) *virtualNode {
	current := sl.dummy
	for i := sl.totalLevel - 1; i >= 0; i-- {
		for current.next[i] != nil && current.next[i].value <= value {
			current = current.next[i]
		}
	}
	return current.next[0]
}

// 删除跳表中的值
func (sl *skipList) Delete(value uint64) {
	update := sl.cached[:sl.totalLevel]
	current := sl.dummy

	for i := sl.totalLevel - 1; i >= 0; i-- {
		for current.next[i] != nil && current.next[i].value < value {
			current = current.next[i]
		}
		update[i] = current
	}

	current = current.next[0]
	if current != nil && current.value == value {
		for i := 0; i < sl.totalLevel; i++ {
			if update[i].next[i] != current {
				break
			}
			update[i].next[i] = current.next[i]
		}

		for sl.totalLevel > 1 && sl.dummy.next[sl.totalLevel-1] == nil {
			sl.totalLevel--
		}
	}
}

// consistent hash
type ConsistInfo struct {
	mu        sync.RWMutex
	key       string
	realNodes []discovery.Instance
	l         *skipList
	// for hash
	maxLen int
}

func NewConsistInfo(result discovery.Result) *ConsistInfo {
	info := &ConsistInfo{l: newSkipList(), realNodes: result.Instances}
	for _, ins := range result.Instances {
		addAllVirtual(info, &instanceNode{ins})
	}
	return info
}

func (info *ConsistInfo) getConsistentResult(value uint64) discovery.Instance {
	info.mu.RLock()
	defer info.mu.RUnlock()

	n := info.l.FindGreater(value)
	if n == nil || n.realNode == nil {
		return info.l.dummy.next[0].realNode
	}
	return n.realNode.Instance
}

func (info *ConsistInfo) Rebalance(change discovery.Change) {
	info.mu.Lock()
	defer info.mu.Unlock()

	for _, ins := range change.Added {
		addAllVirtual(info, &instanceNode{ins})
	}
	for _, ins := range change.Removed {
		removeAllVirtual(info, &instanceNode{ins})
	}
	// TODO: change.Updated
}

func removeAllVirtual(info *ConsistInfo, node *instanceNode) {
	l := getVirtualNodeLen(node)
	maxAddrLength := 45
	b := make([]byte, 0, utils.GetUIntLen(uint64(l))+maxAddrLength+1)
	addrByte := utils.StringToSliceByte(node.Address().String())

	for i := 0; i < l; i++ {
		vv := getVirtualNodeHash(b, addrByte, i)
		info.l.Delete(vv)
	}
}

func addAllVirtual(info *ConsistInfo, node *instanceNode) {
	l := getVirtualNodeLen(node)
	maxAddrLength := 45
	b := make([]byte, 0, utils.GetUIntLen(uint64(l))+maxAddrLength+1)
	addrByte := utils.StringToSliceByte(node.Address().String())

	for i := 0; i < l; i++ {
		vv := getVirtualNodeHash(b, addrByte, i)
		info.l.Insert(&virtualNode{realNode: node, value: vv})
	}
}

func getVirtualNodeHash(b []byte, addr []byte, idx int) uint64 {
	b = append(b, addr...)
	b = append(b, '#')
	b = append(b, byte(idx))
	return xxhash3.Hash(b)
}

func getVirtualNodeLen(node *instanceNode) int {
	virtualFactor := 100
	return node.Weight() * virtualFactor
}
