package localaitools

import "context"

func (f *fakeClient) ListPluginMarketplaces(_ context.Context) ([]PluginMarketplace, error) {
	f.record("ListPluginMarketplaces", nil)
	return []PluginMarketplace{}, nil
}

func (f *fakeClient) AddPluginMarketplace(_ context.Context, source, ref, id string) (*PluginMarketplace, error) {
	f.record("AddPluginMarketplace", []string{source, ref, id})
	return &PluginMarketplace{ID: id, Location: source}, nil
}

func (f *fakeClient) RefreshPluginMarketplace(_ context.Context, id string) (*PluginMarketplace, error) {
	f.record("RefreshPluginMarketplace", id)
	return &PluginMarketplace{ID: id}, nil
}

func (f *fakeClient) BrowsePlugins(_ context.Context, q PluginBrowseQuery) (*PluginCatalog, error) {
	f.record("BrowsePlugins", q)
	if f.browsePlugins != nil {
		return f.browsePlugins(q)
	}
	return &PluginCatalog{Items: []PluginCatalogItem{}}, nil
}

func (f *fakeClient) ListPluginInstalls(_ context.Context, all bool) ([]PluginInstall, error) {
	f.record("ListPluginInstalls", all)
	if f.listPluginInstalls != nil {
		return f.listPluginInstalls(all)
	}
	return []PluginInstall{}, nil
}

func (f *fakeClient) InstallPlugin(_ context.Context, plugin, scope string) (*PluginInstallResult, error) {
	f.record("InstallPlugin", []string{plugin, scope})
	if f.installPlugin != nil {
		return f.installPlugin(plugin, scope)
	}
	return &PluginInstallResult{JobID: "job-p", Plugin: plugin}, nil
}

func (f *fakeClient) CheckPluginUpdate(_ context.Context, id string) (*PluginUpdateCheck, error) {
	f.record("CheckPluginUpdate", id)
	return &PluginUpdateCheck{Install: id}, nil
}

func (f *fakeClient) SetPluginEnabled(_ context.Context, id string, on bool) (*PluginState, error) {
	f.record("SetPluginEnabled", on)
	return &PluginState{Install: id, Enabled: on}, nil
}

func (f *fakeClient) RemovePlugin(_ context.Context, id string) error {
	f.record("RemovePlugin", id)
	return nil
}
