package utils

import "testing"

func BenchmarkRandUserAgent(b *testing.B) {
	for i := 0; i < b.N; i++ {
		RandUserAgent()
	}
}

func BenchmarkGenerateRandomUA(b *testing.B) {
	for i := 0; i < b.N; i++ {
		GenerateRandomUA()
	}
}
