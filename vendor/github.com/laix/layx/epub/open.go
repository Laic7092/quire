package epub

// Open reads an EPUB file and returns its reading order.
//
// This module has no plain-text path: a text converter belongs to the caller,
// not to the EPUB container reader.
func Open(file string) (*Book, error) {
	return openEPUB(file)
}
