/*
 * Copyright 2020 Torben Schinke
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package sqlm

import (
	"fmt"
	"strings"
)

type writer struct {
	sb      *strings.Builder
	indent  int
	newLine bool
}

func newWriter() *writer {
	return &writer{sb: &strings.Builder{}}
}

func (w *writer) Indent(i int) {
	w.indent += i
}

func (w *writer) ShiftLeft() {
	w.Indent(-2)
}

func (w *writer) ShiftRight() {
	w.Indent(2)
}

func (w *writer) Printf(str string, args ...interface{}) {
	if w.newLine {
		for i := 0; i < w.indent; i++ {
			w.sb.WriteByte(' ')
		}
	}
	w.sb.WriteString(fmt.Sprintf(str, args...))
	w.newLine = strings.HasSuffix(str, "\n")
}

func (w *writer) String() string {
	return w.sb.String()
}
