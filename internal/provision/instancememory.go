package provision

// InstanceMemoryGiB returns the memory in GiB for the given EC2 instance type.
// Returns 0 for unknown types so callers can apply a fallback.
func InstanceMemoryGiB(instanceType string) int {
	return instanceMemoryMap[instanceType]
}

// instanceMemoryMap covers the EC2 families used for AI desktops.
// Values are in GiB (fractional sizes like t3.nano's 0.5 GiB are rounded up to 1).
var instanceMemoryMap = map[string]int{
	// t3 family
	"t3.nano":    1,
	"t3.micro":   1,
	"t3.small":   2,
	"t3.medium":  4,
	"t3.large":   8,
	"t3.xlarge":  16,
	"t3.2xlarge": 32,
	// t3a family
	"t3a.nano":    1,
	"t3a.micro":   1,
	"t3a.small":   2,
	"t3a.medium":  4,
	"t3a.large":   8,
	"t3a.xlarge":  16,
	"t3a.2xlarge": 32,
	// t4g family (ARM)
	"t4g.nano":    1,
	"t4g.micro":   1,
	"t4g.small":   2,
	"t4g.medium":  4,
	"t4g.large":   8,
	"t4g.xlarge":  16,
	"t4g.2xlarge": 32,
	// m5 family
	"m5.large":    8,
	"m5.xlarge":   16,
	"m5.2xlarge":  32,
	"m5.4xlarge":  64,
	"m5.8xlarge":  128,
	"m5.12xlarge": 192,
	"m5.16xlarge": 256,
	"m5.24xlarge": 384,
	// m6i family
	"m6i.large":    8,
	"m6i.xlarge":   16,
	"m6i.2xlarge":  32,
	"m6i.4xlarge":  64,
	"m6i.8xlarge":  128,
	"m6i.12xlarge": 192,
	"m6i.16xlarge": 256,
	"m6i.24xlarge": 384,
	// m7i family
	"m7i.large":   8,
	"m7i.xlarge":  16,
	"m7i.2xlarge": 32,
	"m7i.4xlarge": 64,
	"m7i.8xlarge": 128,
	// c5 family
	"c5.large":    4,
	"c5.xlarge":   8,
	"c5.2xlarge":  16,
	"c5.4xlarge":  32,
	"c5.9xlarge":  72,
	"c5.18xlarge": 144,
	// c6i family
	"c6i.large":   4,
	"c6i.xlarge":  8,
	"c6i.2xlarge": 16,
	"c6i.4xlarge": 32,
	"c6i.8xlarge": 64,
	// r5 family
	"r5.large":   16,
	"r5.xlarge":  32,
	"r5.2xlarge": 64,
	"r5.4xlarge": 128,
	"r5.8xlarge": 256,
	// r6i family
	"r6i.large":   16,
	"r6i.xlarge":  32,
	"r6i.2xlarge": 64,
	"r6i.4xlarge": 128,
	"r6i.8xlarge": 256,
}
