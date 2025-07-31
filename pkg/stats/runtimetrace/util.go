package runtimetrace

import "os"

// fileWriter 是一个实现了 io.Writer 的结构体
type fileWriter struct {
	file *os.File
}

// Write 实现 io.Writer 接口
func (fw *fileWriter) Write(p []byte) (n int, err error) {
	return fw.file.Write(p)
}

// newFileWriter 打开一个文件并返回 fileWriter
func newFileWriter(filename string) (*fileWriter, error) {
	file, err := os.OpenFile(filename, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644) // 如果想追加用 os.OpenFile
	if err != nil {
		return nil, err
	}
	return &fileWriter{file: file}, nil
}

// 记得关闭文件
func (fw *fileWriter) Close() error {
	return fw.file.Close()
}
