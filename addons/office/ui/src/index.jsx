// The entry of the Office add-on. Its work happens in the chats: the tools read
// and edit Word, Excel, PowerPoint and PDF files. This page only says so.
export function createAddon(sdk) {
  const { React } = sdk
  // eslint-disable-next-line no-unused-vars
  const h = React.createElement
  sdk.locale.define('en', {
    'office.title': 'Office',
    'office.intro': 'Your chats can read and edit documents in your files. Ask for a change in a chat and the add-on makes it. If someone changed the file in the meantime, your version is saved as a copy next to it.',
    'office.word': 'Word documents (.docx): read the text, replace text, add paragraphs and tables, create new ones.',
    'office.excel': 'Excel workbooks (.xlsx): read the sheets, set cells and formulas, add sheets, create new ones.',
    'office.powerpoint': 'PowerPoint decks (.pptx): read the slides, edit text and shapes, add slides, create new decks.',
    'office.pdf': 'PDF files (.pdf): read the text and the form fields.',
  })
  const t = (key) => sdk.locale.t(key, key)

  function Component({ page }) {
    if (page !== 'office') return null
    return (
      <section data-testid="office-page" className="office-page">
        <h1 className="office-page__title">{t('office.title')}</h1>
        <p>{t('office.intro')}</p>
        <ul className="office-page__kinds">
          <li>{t('office.word')}</li>
          <li>{t('office.excel')}</li>
          <li>{t('office.powerpoint')}</li>
          <li>{t('office.pdf')}</li>
        </ul>
      </section>
    )
  }
  return { Component, components: {}, chatActions: [], dispose() {} }
}
