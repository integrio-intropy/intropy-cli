package gitops

import "github.com/integrio-intropy/intropy-cli/internal/gitclone"

// SameRepository reports whether two remote URLs name the same repository.
// It is an alias for gitclone.SameRepository; the implementation lives there
// because URL identity is a property of cached clones generally, not of the
// GitOps consumer.
var SameRepository = gitclone.SameRepository
