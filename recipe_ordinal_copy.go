package main

import (
	"fmt"

	"./internal/updateapply"
)

// Copy changed native COW branches into an independently collected owner.
// base is an already complete destination root at the same radix position.
// Reusing unchanged branches avoids repeatedly walking a global pin index.
// Bytes are copied, not hard-linked: native GC uses each owner's page mtimes.
func recipeCopyOrdinalSubtree(from, to *ordinalTree, root, base string, leaf func(ordinalKV) error) error {
	from.checkPath, to.checkPath = updateapply.CheckPath, updateapply.CheckPath
	var copyTree func(string, string, int) error
	copyTree = func(hash, prior string, depth int) error {
		if err := to.ctx.Err(); err != nil {
			return err
		}
		if hash == "" {
			return nil
		}
		if depth > 128 {
			return fmt.Errorf("copied ordinal subtree exceeds radix depth")
		}
		if hash == prior {
			_, err := to.read(hash)
			return err
		}
		page, err := from.read(hash)
		if err != nil {
			return err
		}
		var old ordinalPage
		if prior != "" {
			old, err = to.read(prior)
			if err != nil {
				return err
			}
		}
		for i, child := range page.Children {
			previous := ""
			if len(old.Children) == 16 {
				previous = old.Children[i]
			}
			if err = copyTree(child, previous, depth+1); err != nil {
				return err
			}
		}
		if leaf != nil {
			for _, entry := range page.Entries {
				if err = leaf(entry); err != nil {
					return err
				}
			}
		}
		copied, err := to.write(page)
		if err != nil {
			return err
		}
		if copied != hash {
			return fmt.Errorf("native ordinal page encoding changed during copy")
		}
		return nil
	}
	if err := copyTree(root, base, 0); err != nil {
		return err
	}
	to.root = root
	_, err := to.flush()
	return err
}
