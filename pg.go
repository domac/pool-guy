package poolguy

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"runtime"
	"sync"
	"sync/atomic"
	"time"
)

const (
	DEFAULT_MAX_LIMIT = 1000             //默认最大guy限制
	DEFAULT_IDLE_TIME = 10 * time.Second //默认最大空闲超时时间
	POOL_STATUS_STOP  = 0
	POOL_STATUS_START = 1
)

var ErrNilGuy = errors.New("over size limit, can't fetch it from pool")

//生成go-id
func goid() []byte {
	b := make([]byte, 20)
	rand.Read(b)

	uuid := make([]byte, 40)
	hex.Encode(uuid, b)
	return uuid
}

//调度单元封装
type goguy struct {
	//因为执行函数的时候，不是立刻就去执行的，所以需要等待队列
	ch           chan func()
	lastUsedTime time.Time //上一次完成使用的时间
	id           []byte    //go-id
}

//goroutine池
type Pool struct {
	maxLimit    int           //最大guy限制
	idleTimeout time.Duration //空闲超时时间

	sync.Mutex
	currentCount  int
	localFreeList []*goguy //本地空闲队列
	stopChan      chan struct{}
	status        int32
}

//新建goroutine池
func NewPool(maxLimit int, idleTimeout time.Duration) *Pool {
	if maxLimit <= 0 {
		maxLimit = DEFAULT_MAX_LIMIT
	}

	if idleTimeout <= 0 {
		idleTimeout = DEFAULT_IDLE_TIME
	}

	p := &Pool{
		maxLimit:    maxLimit,
		idleTimeout: idleTimeout,
		status:      POOL_STATUS_STOP,
	}
	//自动调度
	p.autoSchedule()
	return p
}

//开启自动调度
func (p *Pool) autoSchedule() {
	if p.stopChan == nil &&
		atomic.CompareAndSwapInt32(&p.status, POOL_STATUS_STOP, POOL_STATUS_START) {

		p.stopChan = make(chan struct{})
		stopCh := p.stopChan

		go func() {
			var cleanList []*goguy
			for {
				//cleanList可循环使用
				p.autoClean(&cleanList)
				select {
				case <-stopCh:
					return
				default:
					//检查间隔
					time.Sleep(p.idleTimeout)
				}
			}
		}()
	} else {
		panic("goroutine-Pool already started")
	}
}

//后台清理过期的goroutine
func (p *Pool) autoClean(cleanList *[]*goguy) {

	currentCheckTime := time.Now()
	p.Lock()
	localFreeList := p.localFreeList
	localFreeListLength := len(localFreeList)

	//计算出要从第几个开始淘汰
	removeIndex := 0
	for removeIndex < localFreeListLength &&
		currentCheckTime.Sub(localFreeList[removeIndex].lastUsedTime) > p.idleTimeout {
		removeIndex++
	}

	//把需要清理的goguy放到cleanlist去
	*cleanList = append((*cleanList)[:0], localFreeList[:removeIndex]...)

	//如进行过清理，则执行后续操作
	if removeIndex > 0 {
		cidx := copy(localFreeList, localFreeList[removeIndex:])
		for i := cidx; i < localFreeListLength; i++ {
			localFreeList[i] = nil
		}
		p.localFreeList = localFreeList[:cidx]
	}
	tempList := *cleanList
	for i, gg := range tempList {
		gg.ch <- nil
		tempList[i] = nil
		p.currentCount--

	}
	p.Unlock()
}

var getPoolCap = func() int {
	if runtime.GOMAXPROCS(0) == 1 {
		return 0
	}
	return 1
}()

func (p *Pool) Stop() {

	if p.stopChan != nil &&
		atomic.CompareAndSwapInt32(&p.status, POOL_STATUS_START, POOL_STATUS_STOP) {
		close(p.stopChan)
		p.stopChan = nil

		p.Lock()
		localFreeList := p.localFreeList
		for i, gg := range localFreeList {
			gg.ch <- nil
			localFreeList[i] = nil
		}
		p.localFreeList = localFreeList[:0]
		p.currentCount = 0
		p.Unlock()
	} else {
		panic("goroutine-Pool already stopped")
	}
}

func (p *Pool) Go(fn func()) ([]byte, error) {
	gg := p.findGuy()
	if gg == nil {
		return nil, ErrNilGuy
	}
	gg.ch <- fn
	return gg.id, nil
}

func (p *Pool) findGuy() *goguy {

	var gg *goguy
	createSign := false

	p.Lock()

	localFreeList := p.localFreeList
	n := len(localFreeList) - 1

	if n < 0 {
		//空队列，无可用
		if p.currentCount < p.maxLimit {
			createSign = true
			p.currentCount++
		}
	} else {
		//可复用
		gg = localFreeList[n] //从尾部拿取（较新）
		localFreeList[n] = nil
		p.localFreeList = localFreeList[:n]
	}
	p.Unlock()

	if gg == nil {
		//是否需要创建
		if !createSign {
			return nil
		}
		//从全局列表中获取
		gg = &goguy{
			ch: make(chan func(), getPoolCap),
			id: goid(),
		}

		go p.runGuy(gg)
	}
	return gg
}

func (p *Pool) runGuy(gg *goguy) {
	for fn := range gg.ch {
		if fn == nil {
			break
		}
		fn()
		//释放goroutine
		if !p.markRunnable(gg) {
			break
		}
	}

}

//释放goroutine
func (p *Pool) markRunnable(gg *goguy) bool {
	gg.lastUsedTime = time.Now()
	p.Lock()
	if p.status == POOL_STATUS_STOP {
		p.Unlock()
		return false
	}
	//从后追加
	p.localFreeList = append(p.localFreeList, gg)
	p.Unlock()
	return true
}

func (p *Pool) CurrentGoCount() int {
	return p.currentCount
}

//获取当前本地列表的goroutine数量
func (p *Pool) CurrentLocalListSize() int {
	return len(p.localFreeList)
}
