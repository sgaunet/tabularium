package extract

// MinTextLayerRunes is the threshold below which a PDF is treated as carrying
// no usable text layer.
//
// It is exposed because the black-box test needs to build a document that sits
// just either side of it, and hard-coding 64 in the test would let the two
// drift apart silently — which is the one way this classification can go wrong
// without anybody noticing.
const MinTextLayerRunes = minTextLayerRunes

// UsableTextLayer is the emptiness test itself.
var UsableTextLayer = usableTextLayer
