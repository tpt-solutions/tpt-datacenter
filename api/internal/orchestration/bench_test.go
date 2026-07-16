// SPDX-FileCopyrightText: 2024 TPT Solutions
// SPDX-License-Identifier: MIT OR Apache-2.0

package orchestration

import (
	"context"
	"testing"
)

func BenchmarkSubmit(b *testing.B) {
	orc := New(NewSimSink(), DefaultPolicy())
	ctx := context.Background()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = orc.Submit(ctx, SubmitRequest{
			Device: "rack-01", Command: CmdValve, Value: 42.0, Source: "bench",
		})
	}
}

func BenchmarkSubmitParallel(b *testing.B) {
	orc := New(NewSimSink(), DefaultPolicy())
	ctx := context.Background()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			_, _ = orc.Submit(ctx, SubmitRequest{
				Device: "rack-01", Command: CmdFan, Value: 33.0, Source: "bench",
			})
		}
	})
}
