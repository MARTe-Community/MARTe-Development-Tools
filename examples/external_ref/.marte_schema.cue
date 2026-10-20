package schema

// Project schema for examples/external_ref: declares the site-specific classes
// the configuration uses (NI6368 ADC, DAN, EPICS output, UDP streamer and the
// custom GAMs). They are open structs on purpose: the example only relies on
// the fields shown in ex.marte.

#Classes: {
	"NI6368ADC": { // config spells it "NI6368::NI6368ADC": {
		Class!: string
		...
	}
	"NI6683H": {
		Class!: string
		...
	}
	"DANSource": { // config spells it "DAN::DANSource": {
		Class!: string
		...
	}
	"EPICSCAOutput": { // config spells it "EPICSCA::EPICSCAOutput": {
		Class!: string
		...
	}
	"UDPStreamer": { // config spells it "IOExt::UDPStreamer": {
		Class!: string
		...
	}
	"RealTimeThreadAsyncBridge": {
		Class!: string
		...
	}
	"AbsoluteTimeGeneratorGAM": {
		Class!: string
		...
	}
	"PulseDetectorGAM": {
		Class!: string
		...
	}
	"JAConditionalSignalUpdateGAM": {
		Class!: string
		...
	}
}
