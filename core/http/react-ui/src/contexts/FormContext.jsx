import { createContext, useContext, useMemo } from 'react'

// FormContext exposes the surrounding form's read-only state to deep
// field editors that need to inspect sibling fields. Used by the
// router-candidates editor to read router.policies so candidate
// labels can be picked from the declared policy vocabulary rather
// than typed by hand.
//
// Only the read shape is exposed (formData); mutations still go
// through the parent's onChange so the editor remains the single
// source of truth.
const FormContext = createContext(null)

export function FormContextProvider({ formData, children }) {
  // Memo the wrapper so consumers don't re-render on every keystroke
  // when formData itself is referentially stable. ModelEditor's
  // setValues replaces the object on each edit, so this still
  // propagates updates — it just avoids spurious churn when an
  // ancestor re-renders without changing values.
  const value = useMemo(() => ({ formData }), [formData])
  return <FormContext.Provider value={value}>{children}</FormContext.Provider>
}

export function useFormContext() {
  return useContext(FormContext)
}
