---
name: pdf-processing
description: Extract text and tables from PDF files, fill PDF forms and merge documents. Use when the user mentions PDFs, forms or document extraction.
license: MIT
metadata:
  author: agentkit-examples
  version: "1.0"
---

# PDF processing

1. Identify what the user needs: extraction, form filling or merging.
2. For extraction, prefer text layers over OCR; fall back to OCR only for scans.
3. Report page numbers with every quoted passage.

See [the reference](references/REFERENCE.md) for the form-field naming conventions.
