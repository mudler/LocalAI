import { useLayoutEffect, useRef } from 'react'
import Button from './Button'
import { sliderFill, sliderText, sliderValue } from './sliderModel'

// The kit slider: a native range input (arrow keys, Home, End, Page Up and
// Page Down work as for any range) with a number box beside it and a reset
// button. The value is a string, so "not set" ('') is a state of its own: the
// box is empty, shows `placeholder`, and the thumb rests at the start of the
// track, dimmed. Reset returns to that state.
//
//   id            id of the number box, for a <label htmlFor>
//   range         { min, max, step, integer, exclusiveMin }: see SETTING_RANGES
//   value         the text of the value ('' when not set)
//   onChange(v)   called with the new text
//   label         accessible name of the slider and the box
//   placeholder   what the empty box says ("Model default")
//   resetLabel    accessible name and tooltip of the reset button
//   invalid       the box holds a value the setting does not accept
//   describedBy   id of the hint or error that describes the control
//   disabled
//   testId        prefix: <testId>, <testId>-range, <testId>-input, <testId>-reset
export default function Slider({ id, range, value, onChange, label, placeholder, resetLabel, invalid = false, describedBy, disabled = false, testId }) {
  const position = sliderValue(range, value)
  const set = value !== null && value !== undefined && String(value).trim() !== ''
  const low = range.exclusiveMin ? range.step : range.min
  // The filled part of the track is a custom property the kit's CSS reads.
  const rangeRef = useRef(null)
  const fill = sliderFill(range, position)
  useLayoutEffect(() => { rangeRef.current?.style.setProperty('--dk-fill', `${fill}%`) }, [fill])
  return (
    <div className="ui-slider" data-unset={set ? undefined : ''} data-testid={testId}>
      <input
        ref={rangeRef}
        type="range"
        className="dk-range ui-slider__range"
        min={low}
        max={range.max}
        step={range.step}
        value={position ?? low}
        disabled={disabled}
        aria-label={label}
        aria-describedby={describedBy}
        aria-valuetext={set ? String(value) : placeholder}
        onChange={(e) => onChange(sliderText(range, e.target.value))}
        data-testid={testId ? `${testId}-range` : undefined}
      />
      <input
        id={id}
        className="dk-input dk-input--mono ui-slider__box"
        inputMode={range.integer ? 'numeric' : 'decimal'}
        value={value ?? ''}
        placeholder={placeholder}
        disabled={disabled}
        aria-invalid={invalid ? true : undefined}
        aria-describedby={describedBy}
        autoComplete="off"
        spellCheck={false}
        onChange={(e) => onChange(e.target.value)}
        data-testid={testId ? `${testId}-input` : undefined}
      />
      <Button
        variant="ghost"
        icon="undo"
        aria-label={resetLabel}
        title={resetLabel}
        disabled={disabled || !set}
        onClick={() => onChange('')}
        data-testid={testId ? `${testId}-reset` : undefined}
      />
    </div>
  )
}
