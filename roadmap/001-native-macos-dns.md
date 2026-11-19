# Use native macOS DNS resolver discovery

Advanced DNS and ECH currently discover macOS scoped resolvers by parsing `scutil --dns`. Replace this with system APIs, preserving per-domain resolver selection, cancellation and error handling without a subprocess or text parser.
