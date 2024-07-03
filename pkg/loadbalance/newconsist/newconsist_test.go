package newconsist

import (
	"fmt"
	"github.com/bytedance/gopkg/util/xxhash3"
	"github.com/cloudwego/kitex/internal/test"
	"github.com/cloudwego/kitex/pkg/discovery"
	"math/rand"
	"strconv"
	"testing"
	"time"
)

func TestNewSkipList(t *testing.T) {
	s := newSkipList()
	dataCnt := 1000
	for i := 0; i < dataCnt; i++ {
		s.Insert(&virtualNode{realNode: nil, value: uint64(i)})
		test.Assert(t, s.Search(uint64(i)))
	}
	totalCnt := 0
	for i := 0; i < s.totalLevel; i++ {
		currCnt := countLevel(s, i)
		totalCnt += currCnt
		fmt.Printf("level[%d], count[%d]\n", i, currCnt)
	}
	fmt.Printf("totalCnt: %d, ratio: %f\n", totalCnt, float64(totalCnt)/float64(dataCnt))
	for i := 0; i < 1000; i++ {
		s.Delete(uint64(i))
	}
	dataCnt -= 1000
	currCnt := countLevel(s, 0)
	fmt.Printf("totalCnt, expected=%d, actual=%d\n", dataCnt, currCnt)
}

func countLevel(s *skipList, level int) int {
	n := s.dummy
	cnt := 0
	for n.next[level] != nil {
		n = n.next[level]
		cnt++
	}
	return cnt
}

func TestFuzzSkipList(t *testing.T) {
	rand.Seed(time.Now().UnixNano())
	sl := newSkipList()

	vs := make([]uint64, 1000)
	// 插入1000个随机值
	for i := 0; i < 1000; i++ {
		value := rand.Uint64() % 10000
		vs[i] = value
		node := newNode(nil, value, randomLevel())
		sl.Insert(node)
	}

	// 搜索1000个随机值
	for i := 0; i < 1000; i++ {
		found := sl.Search(vs[i])
		test.Assert(t, found)
	}

	// 删除500个随机值
	for i := 0; i < 500; i++ {
		value := vs[i]
		sl.Delete(value)
	}
	for i := 500; i < 1000; i++ {
		found := sl.Search(vs[i])
		test.Assert(t, found)
	}
}

func Test_getConsistentResult(t *testing.T) {
	insList := []discovery.Instance{
		discovery.NewInstance("tcp", "addr1", 10, nil),
		discovery.NewInstance("tcp", "addr2", 10, nil),
		discovery.NewInstance("tcp", "addr3", 10, nil),
		discovery.NewInstance("tcp", "addr4", 10, nil),
		discovery.NewInstance("tcp", "addr5", 10, nil),
	}
	e := discovery.Result{
		Cacheable: false,
		CacheKey:  "",
		Instances: insList,
	}
	info := NewConsistInfo(e)
	newInsList := make([]discovery.Instance, len(insList)-1)
	copy(newInsList, insList[1:])
	newResult := discovery.Result{
		Cacheable: false,
		CacheKey:  "",
		Instances: newInsList,
	}
	change := discovery.Change{
		Result:  newResult,
		Added:   nil,
		Removed: []discovery.Instance{insList[0]},
		Updated: nil,
	}
	_ = change
	//info.Rebalance(change)
	cnt := make(map[string]int)
	for _, ins := range insList {
		cnt[ins.Address().String()] = 0
	}
	cnt["null"] = 0
	for i := 0; i < 100000; i++ {
		if res := info.getConsistentResult(xxhash3.HashString(strconv.Itoa(i))); res != nil {
			cnt[res.Address().String()]++
		} else {
			cnt["null"]++
		}
	}
	fmt.Println(cnt)
}
