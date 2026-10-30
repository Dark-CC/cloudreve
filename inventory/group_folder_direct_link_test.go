package inventory

import (
	"testing"

	"github.com/cloudreve/Cloudreve/v4/inventory/types"
	"github.com/stretchr/testify/require"
)

// FolderDirectLink is a grant: any membership that turns it on grants it,
// the same way RedirectedSource and AllowSelectNode do.
func TestMergeFolderDirectLink(t *testing.T) {
	t.Run("either side grants", func(t *testing.T) {
		dst := &types.GroupSetting{}
		mergeGroupSettings(dst, &types.GroupSetting{FolderDirectLink: true})
		require.True(t, dst.FolderDirectLink)

		dst = &types.GroupSetting{FolderDirectLink: true}
		mergeGroupSettings(dst, &types.GroupSetting{})
		require.True(t, dst.FolderDirectLink)
	})

	t.Run("neither side grants", func(t *testing.T) {
		dst := &types.GroupSetting{}
		mergeGroupSettings(dst, &types.GroupSetting{})
		require.False(t, dst.FolderDirectLink)
	})

	t.Run("nil src is a no-op", func(t *testing.T) {
		dst := &types.GroupSetting{FolderDirectLink: true}
		mergeGroupSettings(dst, nil)
		require.True(t, dst.FolderDirectLink)
	})
}
