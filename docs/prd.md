## Purpose

I want to build a tool that keeps track of my worktrees for a specific repository. It should understand when a repository is in github and keep track of which PRs are open.

Whenever a PR is opened in a repository that lumberjack is tracking it should create a new worktree to track that branch.

If our repo is in /path/to/my_repo, I want you to create new worktree clones in /path/to/my_repo-branch-name. Obviously some branch names aren't going to be valid file names, here's some examples of how we should resolve this.

| Branch name          | Directory name     |
| -------------------- | ------------------ |
| feature/my-feature   | my_repo-my-feature |
| my-feature           | my_repo-my-feature |
| fix/#12345-bugfix-id | my_repo-bugfix-id  |

Lumberjack should be initialised in a repository by doing the following

```
cd /path/to/my_repo
lumberjack init .
```

Once this is done, it should check to see if this is a github repository, if it is, then it should go through a process to authenticate with the github API and establish that it can get the status of PRs.

Once this has been established then lumberjack should store the context of this repository in a database, so that it knows which repos it is tracking.

A background daemon process should query this database on an hourly basis and check for open PRs. Any PRs that are open should have their branches downloaded and checked out as described above. Any branches that are currently checked out but their PRs have been merged or closed should be removed. If a worktree has changes locally that are not in the merged branch, then it shouldn't be deleted, these should be considered in need of reconciliation.

## Commands

`lumberjack repositories` should show the list of tracked repositories
`lumberjack repository NAME` should show details of the last sync for a named repository
`lumberjack repository NAME worktrees` should show a list of worktrees checked out for a repository, with their path, and if they need reconciliation, a warning
`lumberjack repository NAME worktree BRANCH_OR_DIRECTORY_NAME delete` will delete the worktree, if the tip of the local worktree doesn't match the tip merged remote, then we should ask for confirmation with a warning that the user will lose X commits
`lumberjack repositories --sync` Triggers a synchronisation of all repositories
`lumberjack sync` Within the context of a tracked repo synchronises worktrees for that repo specifically
`lumberjack doctor` Checks that the required host prerequisites are available and reports their location and version. It verifies that `git` and `gh` can be found (honouring `LUMBERJACK_GIT_PATH` and `LUMBERJACK_GITHUB_CLI_PATH`, otherwise searching the system `PATH`), and that `gh` is authenticated. Exits non-zero if any check fails, so it can be used in scripts.

## Environment variables

| Variable             | Default                   | Description                                                                                                            |
| -------------------- | ------------------------- | ---------------------------------------------------------------------------------------------------------------------- |
| `LUMBERJACK_DB_PATH` | `~/.lumberjack/db.sqlite` | Path to the SQLite database tracking repositories and worktrees. The parent directory is created if it does not exist. |
| `LUMBERJACK_GIT_PATH` | Located on `PATH` (`git`) | Path to the `git` executable. If unset, the system `PATH` is searched. |
| `LUMBERJACK_GITHUB_CLI_PATH` | Located on `PATH` (`gh`) | Path to the GitHub CLI (`gh`) executable. If unset, the system `PATH` is searched. |
