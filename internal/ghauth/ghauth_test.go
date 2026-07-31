package ghauth

import (
	"context"
	"reflect"
	"testing"
)

func TestEnvWithoutTokenLeavesBaseUntouched(t *testing.T) {
	base := []string{"PATH=/bin"}
	if got := Env(context.Background(), base); !reflect.DeepEqual(got, base) {
		t.Errorf("Env() = %v, want %v", got, base)
	}
}

func TestWithTokenIgnoresEmptyToken(t *testing.T) {
	ctx := WithToken(context.Background(), "github.com", "")
	if got := Env(ctx, nil); got != nil {
		t.Errorf("Env() = %v, want nil", got)
	}
}

func TestEnvNamesTheVariablePerHost(t *testing.T) {
	for _, tc := range []struct {
		name string
		host string
		want string
	}{
		{name: "dotcom", host: "github.com", want: "GH_TOKEN=t"},
		{name: "empty host", host: "", want: "GH_TOKEN=t"},
		{name: "enterprise", host: "ghe.acme.corp", want: "GH_ENTERPRISE_TOKEN=t"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := WithToken(context.Background(), tc.host, "t")
			got := Env(ctx, nil)
			if len(got) != 1 || got[0] != tc.want {
				t.Errorf("Env() = %v, want [%s]", got, tc.want)
			}
		})
	}
}

func TestEnvAppendsAfterBaseSoItWins(t *testing.T) {
	ctx := WithToken(context.Background(), "github.com", "new")
	got := Env(ctx, []string{"GH_TOKEN=old", "PATH=/bin"})
	want := []string{"GH_TOKEN=old", "PATH=/bin", "GH_TOKEN=new"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Env() = %v, want %v", got, want)
	}
}

func TestWithTokenOverridesAnEarlierToken(t *testing.T) {
	ctx := WithToken(context.Background(), "github.com", "first")
	ctx = WithToken(ctx, "ghe.acme.corp", "second")
	got := Env(ctx, nil)
	if len(got) != 1 || got[0] != "GH_ENTERPRISE_TOKEN=second" {
		t.Errorf("Env() = %v, want [GH_ENTERPRISE_TOKEN=second]", got)
	}
}
