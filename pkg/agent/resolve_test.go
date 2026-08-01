package agent

import "testing"

func TestResolveVPCName(t *testing.T) {
	tests := []struct {
		name       string
		annotation string
		defaultVPC string
		want       string
		wantErr    bool
	}{
		{name: "annotation wins", annotation: "vpc-blue", defaultVPC: "default", want: "vpc-blue"},
		{name: "fallback to default", annotation: "", defaultVPC: "default", want: "default"},
		{name: "no annotation no default", annotation: "", defaultVPC: "", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ResolveVPCName(tt.annotation, tt.defaultVPC)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got != tt.want {
				t.Fatalf("got %q want %q", got, tt.want)
			}
		})
	}
}
