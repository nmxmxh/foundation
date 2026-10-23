//go:build !(amd64 && goexperiment.simd)

package hermes

func scanDelimiter2Kernel(s []byte, b1, b2 byte) int {
	return scanDelimiter2SWAR(s, b1, b2)
}

func scanDelimiter4Kernel(s []byte, b1, b2, b3, b4 byte) int {
	return scanDelimiter4SWAR(s, b1, b2, b3, b4)
}

func scanByteKernel(s []byte, target byte) int {
	return scanByteSWAR(s, target)
}
