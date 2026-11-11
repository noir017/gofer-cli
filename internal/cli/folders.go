package cli

import (
	"fmt"
	"strings"

	"github.com/noir017/gofer-cli/internal/model"
)

// folderNames maps folder ids to the names shown in listings.
func folderNames(folders []model.Folder) map[string]string {
	names := make(map[string]string, len(folders))
	for _, folder := range folders {
		names[folder.ID] = folder.Name
	}
	return names
}

// skippedFolderIDs returns the folders a message that arrived never sits in:
// sent, drafts and trash. otp, link and wait look for mail that arrived, so
// the copy the caller sent, or threw away, must not match.
func skippedFolderIDs(folders []model.Folder) map[string]bool {
	ids := make(map[string]bool)
	for _, folder := range folders {
		switch strings.ToLower(folder.Role) {
		case "sent", "drafts", "trash":
			ids[folder.ID] = true
		}
	}
	return ids
}

// withoutTrash drops the rows that sit in a trash folder. A conversation row
// stays: the received mail inside it may sit elsewhere.
func withoutTrash(refs []model.MessageRef, folders []model.Folder) []model.MessageRef {
	trash := make(map[string]bool)
	for _, folder := range folders {
		if strings.ToLower(folder.Role) == "trash" {
			trash[folder.ID] = true
		}
	}
	if len(trash) == 0 {
		return refs
	}
	kept := refs[:0]
	for _, ref := range refs {
		if !ref.Thread && trash[ref.FolderID] {
			continue
		}
		kept = append(kept, ref)
	}
	return kept
}

// folderChoices names the folders an account has, for an error that has to
// tell the caller what it could have typed instead.
func folderChoices(folders []model.Folder) string {
	if len(folders) == 0 {
		return "none"
	}
	parts := make([]string, 0, len(folders))
	for _, folder := range folders {
		parts = append(parts, fmt.Sprintf("%s (%s)", folder.Name, folder.ID))
	}
	return strings.Join(parts, ", ")
}

// foldersOf keeps the folders of one account.
func foldersOf(folders []model.Folder, accountID string) []model.Folder {
	out := make([]model.Folder, 0, len(folders))
	for _, folder := range folders {
		if folder.AccountID == accountID {
			out = append(out, folder)
		}
	}
	return out
}
