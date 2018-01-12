package poolguy

import (
	"sync"
	"testing"
	"time"
)

func TestGoGuyPool(t *testing.T) {
	pool := NewPool(100, 1*time.Second)
	var wg sync.WaitGroup
	retryTimes := 0
	for i := 0; i < 100; i++ {
		wg.Add(1)
		a := i
		_, err := pool.Go(func() {
			t.Logf("hey, guy : %d \n", a)
			wg.Done()
		})

		if err != nil {
			retryTimes++
			i--
			t.Log(err)
			wg.Done()
		}
	}
	wg.Wait()
	t.Logf("before stop local list size : %d\n", pool.CurrentLocalListSize())
	t.Logf("before stop go count : %d\n", pool.CurrentGoCount())
	pool.Stop()
	t.Logf("after stop local list size : %d\n", pool.CurrentLocalListSize())
	t.Logf("after stop go count : %d\n", pool.CurrentGoCount())
	t.Logf("retryTimes: %d", retryTimes)
}
