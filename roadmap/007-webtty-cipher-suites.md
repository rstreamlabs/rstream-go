# Extend WebTTY payload algorithms

Additional payload algorithms require reviewed implementations, shared client/server vectors and explicit suite negotiation. Keep unsupported algorithms rejected until Go, JavaScript and C++ interoperability is verified for the supporting runtimes. Preserve the restricted FIPS profile and keep authentication, replay and downgrade tests in the release checks.
