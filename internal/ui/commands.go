package ui

// Asynchronous commands: the frame submits and returns, the runtime's answer
// comes back through postToUI. These are the calls that touch a database, a
// model or a channel restart (P0-2): Dispatch, CreateWorkspace,
// NewConversation and SettingsSaver.SaveSettings. A frame must never wait for
// them (design §6.1), so each is one seam a headless test can answer at once:
// runOffMain runs inline when there is no window, and on a goroutine in the real
// app.

import (
	"strings"

	"github.com/zeokeel/super-agent/internal/view"
)

// runOffMain runs fn now in a headless run (tests have no event loop to protect,
// so they stay deterministic) and on a goroutine when a window exists.
func (a *App) runOffMain(fn func()) {
	if a.win == nil {
		fn()
		return
	}
	go fn()
}

// The command seams; tests replace them to answer at once or to hold a command
// pending (so the pending UI can be asserted).
var (
	startDispatch = func(a *App, text string) {
		a.runOffMain(func() {
			var err error
			if a.cmd != nil {
				err = a.cmd.Dispatch(text)
			}
			a.postToUI(func() { a.dispatchDone(text, err) })
		})
	}
	startCreateWorkspace = func(a *App, name, root, purpose, kind string) {
		a.runOffMain(func() {
			creator, ok := a.workspaceCreator()
			var err error
			if !ok || creator == nil {
				err = errNoWorkspaceCreator{}
			} else {
				err = creator.CreateWorkspace(name, root, purpose, kind)
			}
			a.postToUI(func() { a.workspaceCreated(err) })
		})
	}
	startNewConversation = func(a *App, wsID, text string) {
		a.runOffMain(func() {
			creator, ok := a.conversationCreator()
			var err error
			if !ok || creator == nil {
				err = errNoConversationCreator{}
			} else {
				err = creator.NewConversation(wsID, text)
			}
			a.postToUI(func() { a.conversationCreated(err) })
		})
	}
	startRenameWorkspace = func(a *App, id, name string) {
		a.runOffMain(func() {
			renamer, ok := a.workspaceRenamer()
			var err error
			if !ok || renamer == nil {
				err = errNoWorkspaceRenamer{}
			} else {
				err = renamer.RenameWorkspace(id, name)
			}
			a.postToUI(func() { a.workspaceRenamed(err) })
		})
	}
	startDeleteWorkspace = func(a *App, id string) {
		a.runOffMain(func() {
			deleter, ok := a.workspaceDeleter()
			var err error
			if !ok || deleter == nil {
				err = errNoWorkspaceDeleter{}
			} else {
				err = deleter.DeleteWorkspace(id)
			}
			a.postToUI(func() { a.workspaceDeleted(err) })
		})
	}
	// startSaveSettings runs one group's save off the frame. onDone rev is the
	// new revision, 0 when the saver failed or is absent.
	startSaveSettings = func(a *App, group string, values map[string]string, onDone func(rev uint64, err error)) {
		a.runOffMain(func() {
			saver, ok := a.cmd.(SettingsSaver)
			var rev uint64
			var err error
			if !ok || saver == nil {
				err = errNoSettingsSaver{}
			} else {
				rev, err = saver.SaveSettings(group, values)
			}
			a.postToUI(func() { onDone(rev, err) })
		})
	}
)

type errNoWorkspaceCreator struct{}

func (errNoWorkspaceCreator) Error() string {
	return "暂时不可用：运行时还没有接入 WorkspaceCreator"
}

type errNoConversationCreator struct{}

func (errNoConversationCreator) Error() string {
	return "暂时不可用：运行时还没有接入 ConversationCreator"
}

type errNoWorkspaceRenamer struct{}

func (errNoWorkspaceRenamer) Error() string {
	return "暂时不可用：运行时还没有接入 WorkspaceRenamer"
}

type errNoWorkspaceDeleter struct{}

func (errNoWorkspaceDeleter) Error() string {
	return "暂时不可用：运行时还没有接入 WorkspaceDeleter"
}

type errNoSettingsSaver struct{}

func (errNoSettingsSaver) Error() string {
	return "暂时不可用：运行时还没有接入 SettingsSaver"
}

// submit sends the composer's line to the visible target. On a send failure the
// text is kept (draft restored) and 再次发送 is offered; on success the chat
// projection supplies the answer.
func (a *App) submit() {
	text := strings.TrimSpace(a.draft)
	if text == "" || a.cmd == nil || a.dispatchPending {
		return
	}
	target := a.targetWS
	a.draft = ""
	a.composerErr = ""
	a.composerNote = ""
	a.sendFailedText = ""
	a.sendFailedErr = ""
	a.dispatchEcho = text
	a.dispatchPending = true
	if target != "" {
		startDispatchTo(a, target, text)
		return
	}
	startDispatch(a, text)
}

// resend sends the text of a failed send again.
func (a *App) resend() {
	if strings.TrimSpace(a.sendFailedText) == "" {
		return
	}
	a.draft = a.sendFailedText
	a.submit()
}

// dispatchDone applies the runtime's answer to a submitted line. A refusal is the
// agent's answer (neutral); a transport failure keeps the text and says so.
func (a *App) dispatchDone(text string, err error) {
	a.dispatchPending = false
	if err == nil {
		a.composerNote = ""
		a.sendFailedErr = ""
		return
	}
	msg := strings.TrimSpace(err.Error())
	if strings.HasPrefix(msg, "暂时不可用") || strings.Contains(msg, "没有发出去") {
		// A capability gap is not the person's text being wrong: keep the draft
		// and let them retry after the runtime is ready.
		if strings.TrimSpace(a.draft) == "" {
			a.draft = text
		}
		a.sendFailedText = text
		a.sendFailedErr = msg
		return
	}
	// The agent answered the message even if it did not become work.
	a.composerNote = msg
}

// Creating a workspace or a conversation keeps the dialog open while pending and
// applies the answer on the main thread.
func (a *App) workspaceCreated(err error) {
	a.wsCreating = false
	if err != nil {
		a.wsErr = err.Error()
		return
	}
	a.wsErr = ""
	a.wsModal = false
}

func (a *App) conversationCreated(err error) {
	a.convCreating = false
	if err != nil {
		a.convErr = err.Error()
		return
	}
	a.convErr = ""
	a.convModal = false
}

// workspaceRenamed/Deleted apply P1-6's answers; a deleted workspace stops
// filtering any page.
func (a *App) workspaceRenamed(err error) {
	a.renamePending = false
	if err != nil {
		a.renameErr = err.Error()
		return
	}
	a.renameErr = ""
	a.renameOpen = false
}

func (a *App) workspaceDeleted(err error) {
	a.deletePending = false
	if err != nil {
		a.deleteErr = err.Error()
		return
	}
	a.deleteErr = ""
	a.deleteOpen = false
	if a.filterWorkspace == a.deleteID {
		a.filterWorkspace = ""
	}
	if a.workspaceSel == a.deleteID {
		a.workspaceSel = ""
	}
}

// savingSettings is true while a settings group is being saved, so the save bar
// can say 保存中… and a second click cannot send it twice.
func (a *App) savingSettings(g view.SettingsGroupID) bool { return a.savingGroup == string(g) }

// markSaving starts the pending state for a group save.
func (a *App) markSaving(group string) {
	a.savingGroup = group
}
