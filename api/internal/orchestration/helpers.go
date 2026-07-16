// SPDX-FileCopyrightText: 2024 TPT Solutions
// SPDX-License-Identifier: MIT OR Apache-2.0

package orchestration

import (
	"strconv"
	"time"
)

func timeNow() time.Time { return time.Now().UTC() }

func strconvAtoi(s string) (int, error) { return strconv.Atoi(s) }
