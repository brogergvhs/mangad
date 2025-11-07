package downloader

import (
	"io"
)

func copyWithProgress(dst io.Writer, src io.Reader, progress func(done int64)) (int64, error) {
	buf := make([]byte, 32*1024)
	var total int64
	for {
		nr, er := src.Read(buf)

		if nr > 0 {
			nw, ew := dst.Write(buf[0:nr])

			if nw > 0 {
				total += int64(nw)
				if progress != nil {
					progress(total)
				}
			}

			if ew != nil {
				return total, ew
			}

			if nr != nw {
				return total, io.ErrShortWrite
			}
		}

		if er != nil {
			if er == io.EOF {
				break
			}
			return total, er
		}
	}

	return total, nil
}
