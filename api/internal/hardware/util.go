// SPDX-FileCopyrightText: 2024 TPT Solutions
// SPDX-License-Identifier: MIT OR Apache-2.0

package hardware

import (
	"bytes"
	"io"
)

func bytesReader(b []byte) io.Reader {
	return bytes.NewReader(b)
}
