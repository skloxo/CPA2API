package helps

import "sync"

var bufferPool = sync.Pool{
	New: func() any {
		buf := make([]byte, 0, 4096)
		return &buf
	},
}

// GetBuffer 从池中获取一个 buffer
func GetBuffer() *[]byte {
	return bufferPool.Get().(*[]byte)
}

// PutBuffer 将 buffer 归还到池中
func PutBuffer(buf *[]byte) {
	if cap(*buf) > 65536 {
		return // 不归还过大的 buffer
	}
	*buf = (*buf)[:0]
	bufferPool.Put(buf)
}
