package main

func (m *App) Disconnect() {
	m.host.Disconnect()
	m.depTree.closeOverlay()
	m.releaseNotes.closeOverlay()
	if m.metadataCancel != nil {
		m.metadataCancel()
	}
	if m.search.cancel != nil {
		m.search.cancel()
	}
}
