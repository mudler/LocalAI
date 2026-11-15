/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useTranslation } from 'react-i18next'
import { Button } from '../ui'
import { projectFilesPath } from '../../utils/projects'

// The Files tab. The Files page opens a folder from ?path=, so the button goes
// straight to the project's files/ folder.
export default function ProjectFiles({ project }) {
  const { t } = useTranslation('projects')
  const path = projectFilesPath(project)
  return (
    <section className="pj-section" data-testid="project-files">
      <h2 className="pj-h2">{t('files.title')}</h2>
      <p className="pj-note">{t('files.body')}</p>
      <div className="pj-card pj-card--row">
        <div>
          <p className="pj-eyebrow">{t('files.path')}</p>
          <p className="pj-mono" data-testid="project-files-path">{path.slice(1)}</p>
        </div>
        <Button variant="primary" icon="folder" to={`/app/files?path=${encodeURIComponent(path)}`} data-testid="project-files-open">
          {t('files.open')}
        </Button>
      </div>
    </section>
  )
}
