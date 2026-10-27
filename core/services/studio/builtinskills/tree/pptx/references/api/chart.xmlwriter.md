<!-- Generated from pptx 0.2.0 docstrings by griffe2md. Do not edit: fix the docstring in the source. -->

# `pptx.chart.xmlwriter`

Composers for default chart XML for various chart types.

## `ChartXmlWriter`

```python
ChartXmlWriter(chart_type, chart_data)
```

Factory function returning appropriate XML writer object for
*chart_type*, loaded with *chart_type* and *chart_data*.

## `SeriesXmlRewriterFactory`

```python
SeriesXmlRewriterFactory(chart_type, chart_data)
```

Return a `_BaseSeriesXmlRewriter` subclass appropriate to *chart_type*.
