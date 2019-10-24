/*
Copyright 2018 The pdfcpu Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

	http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package pdfcpu

import (
	"reflect"
	"testing"
)

func Test_parseNumericOrIndRef(t *testing.T) {
	type args struct {
		line string
	}
	tests := []struct {
		name    string
		args    args
		want    Object
		wantErr bool
	}{
		{
			name:    "simple Integer",
			args:    args{line: "0"},
			want:    Integer(0),
			wantErr: false,
		},
		{
			name:    "simple Float",
			args:    args{line: "0.0"},
			want:    Float(0.0),
			wantErr: false,
		},
		{
			name:    "simple Integer with white space",
			args:    args{line: "0      "},
			want:    Integer(0),
			wantErr: false,
		},
		{
			name:    "simple Float with white space",
			args:    args{line: "0.0     "},
			want:    Float(0.0),
			wantErr: false,
		},
		{
			name:    "Integer & Float",
			args:    args{line: "0 0.0"},
			want:    Integer(0),
			wantErr: false,
		},
		{
			name:    "Float and Integer",
			args:    args{line: "0.0 0"},
			want:    Float(0.0),
			wantErr: false,
		},
		{
			name:    "Integer, Integer and Integer",
			args:    args{line: "0 0 0"},
			want:    Integer(0),
			wantErr: false,
		},
		{
			name:    "Float, Float and Float",
			args:    args{line: "0.0 0.0 0.0"},
			want:    Float(0.0),
			wantErr: false,
		},
		{
			name:    "simple IndirectRef",
			args:    args{line: "0 0 R"},
			want:    *NewIndirectRef(0, 0),
			wantErr: false,
		},
		{
			name:    "complex IndirectRef",
			args:    args{line: "18446744072028749913 0 R "},
			want:    *NewIndirectRef(0, 0),
			wantErr: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseNumericOrIndRef(&tt.args.line)
			if (err != nil) != tt.wantErr {
				t.Errorf("parseNumericOrIndRef() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("parseNumericOrIndRef() = %v, want %v", got, tt.want)
			}
		})
	}
}
