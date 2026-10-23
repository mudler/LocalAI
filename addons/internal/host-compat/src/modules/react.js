// Stand-in for `react`: the React of the host, handed over by the SDK.
import { getSdk } from '../runtime.js'

const React = getSdk().React
export default React
export const {
  Children, Fragment, Suspense, cloneElement, createContext, createElement, forwardRef, isValidElement, lazy, memo,
  startTransition, useCallback, useContext, useDeferredValue, useEffect, useId, useImperativeHandle, useLayoutEffect,
  useMemo, useReducer, useRef, useState, useSyncExternalStore, useTransition,
} = React
