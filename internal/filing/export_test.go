package filing

// CrossDevice is the EXDEV classifier.
//
// It is exposed because provoking a genuine cross-device rename inside a test
// means arranging two filesystems, which is not something a unit test can do
// portably — while the classification itself is exactly one line of behaviour
// worth pinning down.
var CrossDevice = crossDevice

// TempPrefix is the marker on a partially written file, so the interrupt test
// can look for leftovers by the same name the implementation uses.
const TempPrefix = tempPrefix
