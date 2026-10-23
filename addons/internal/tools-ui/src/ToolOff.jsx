// Shown in place of a Build tool the signed-in person may not use. It replaces a
// silent redirect: it says that the account lacks the permission and who can
// grant it, and points at the tools the person can open. `tool` is 'fineTune' or
// 'quantize'. When `feature` is given and the viewer holds it, the children are
// drawn instead of the notice, so a page wraps its body in this component. The
// way back is the host's home page, so the link leaves the add-on.
export function createToolOff(ctx) {
  const { h, Fragment, t, Icon } = ctx

  function ToolOff({ tool, feature, children }) {
    if (feature && ctx.viewer.hasFeature(feature)) return <>{children}</>
    return (
      <main className="page page--medium bt-page bt-off" data-testid="tool-off" data-cause="account">
        <section className="bt-off__card dk-card">
          <span className="dk-badge dk-badge--warn"><Icon name="lock" /> {t('off.badge')}</span>
          <h1 className="bt-h1">{t(`off.${tool}.title`)}</h1>
          <p className="bt-off__text">{t(`off.${tool}.text`)}</p>
          <p className="dk-hint">{ctx.viewer.isAdmin() ? t('off.admin') : t('off.askAdmin')}</p>
          <div className="bt-off__acts">
            <a className="dk-btn dk-btn--primary" href={`${ctx.basePath}/app`}><Icon name="home" /> {t('off.back')}</a>
          </div>
        </section>
      </main>
    )
  }
  return ToolOff
}
