package drupalorgcli

import (
	"context"
	"fmt"
	"slices"

	"github.com/owenbush/upkeep/internal/gitlab"
)

// RunRawIn is RunRaw with the child started in dir, for the commands that act
// on the git repository they are run inside (issue:setup-remote,
// issue:checkout). An empty dir is refused rather than read as "here": the
// callers always mean a specific working tree, and running git in upkeep's
// own directory by accident would be the wrong repository silently.
func (r *Runner) RunRawIn(ctx context.Context, dir string, args []string, opts ...Option) ([]byte, *gitlab.Failure) {
	if dir == "" {
		return nil, &gitlab.Failure{
			Kind:    gitlab.TransportError,
			Message: fmt.Sprintf("drupalorg %s was not given a directory to run in.", commandName(args)),
		}
	}

	return r.RunRaw(ctx, args, append(slices.Clone(opts), func(i *invocation) { i.dir = dir })...)
}
