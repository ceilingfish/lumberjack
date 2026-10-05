package daemon

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/ceilingfish/lumberjack/internal/sshkey"
	lumberjackv1 "github.com/ceilingfish/lumberjack/pkg/client/lumberjack/v1"
)

func TestSSHKeychainChecksTheRemoteURL(t *testing.T) {
	h := newHarness(t)
	h.git.remoteURL = "git@github.com:o/n.git"
	h.ssh.report = sshkey.Report{LockedKeys: []string{"/k"}, UseKeychainDisabled: true}

	got := h.svc.SSHKeychain(context.Background(), h.repo(t))
	if !slices.Equal(got.LockedKeys, []string{"/k"}) || !got.UseKeychainDisabled {
		t.Errorf("SSHKeychain = %+v", got)
	}
	if !slices.Equal(h.ssh.checked, []string{"git@github.com:o/n.git"}) {
		t.Errorf("checked = %v", h.ssh.checked)
	}
}

func TestSSHKeychainIsEmptyWhenTheRemoteURLFails(t *testing.T) {
	h := newHarness(t)
	h.git.urlErr = errors.New("no remote")

	if got := h.svc.SSHKeychain(context.Background(), h.repo(t)); !got.Empty() {
		t.Errorf("SSHKeychain = %+v, want empty", got)
	}
	if len(h.ssh.checked) != 0 {
		t.Errorf("checked = %v, want none", h.ssh.checked)
	}
}

func TestSSHKeychainIsEmptyWhenTheCheckFails(t *testing.T) {
	h := newHarness(t)
	h.ssh.report = sshkey.Report{LockedKeys: []string{"/k"}}
	h.ssh.err = errors.New("ssh -G failed")

	if got := h.svc.SSHKeychain(context.Background(), h.repo(t)); !got.Empty() {
		t.Errorf("SSHKeychain = %+v, want empty", got)
	}
}

func TestToProtoSSHKeychainOmitsAnEmptyReport(t *testing.T) {
	if got := toProtoSSHKeychain(sshkey.Report{}); got != nil {
		t.Errorf("toProtoSSHKeychain(empty) = %v, want nil", got)
	}
}

func TestServerInitRepositoryReportsSSHKeychain(t *testing.T) {
	h := newHarness(t)
	h.ssh.report = sshkey.Report{LockedKeys: []string{"/k"}}

	resp, err := newServer(h).InitRepository(context.Background(),
		&lumberjackv1.InitRepositoryRequest{LocalPath: h.parent + "/repo"})
	if err != nil {
		t.Fatalf("InitRepository: %v", err)
	}
	if !slices.Equal(resp.GetSshKeychain().GetLockedKeys(), []string{"/k"}) {
		t.Errorf("ssh_keychain = %v", resp.GetSshKeychain())
	}
}

func TestServerSyncReportsSSHKeychain(t *testing.T) {
	h := newHarness(t)
	h.repo(t)
	h.ssh.report = sshkey.Report{UseKeychainDisabled: true}

	stream := &fakeSyncStream{ctx: context.Background()}
	if err := newServer(h).Sync(&lumberjackv1.SyncRequest{Repository: "n"}, stream); err != nil {
		t.Fatalf("Sync: %v", err)
	}
	last := stream.sent[len(stream.sent)-1]
	if !last.GetSummary().GetSshKeychain().GetUseKeychainDisabled() {
		t.Errorf("summary = %v", last.GetSummary())
	}
}
