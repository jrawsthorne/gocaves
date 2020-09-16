package store

import (
	"errors"
	"sync"
	"time"

	"github.com/couchbaselabs/gocaves/mock/mocktime"
)

/**
	TODO(brett19): Describe the implementation below.
**/

// replicator defines a replication system which copies data between vbuckets
// based on a specific replication latency.
type replicator struct {
	chrono      *mocktime.Chrono
	srcVbuckets []*Vbucket
	dstVbuckets []*Vbucket
	latency     time.Duration

	lock        sync.Mutex
	disabled    bool
	hasTimerSet bool
	maxSeqNos   []uint64
}

type replicatorConfig struct {
	Chrono      *mocktime.Chrono
	SrcVbuckets []*Vbucket
	DstVbuckets []*Vbucket
	Latency     time.Duration
}

func newReplicator(config replicatorConfig) (*replicator, error) {
	if len(config.SrcVbuckets) != len(config.DstVbuckets) {
		return nil, errors.New("vbucket counts must match")
	}

	maxSeqNos := make([]uint64, len(config.SrcVbuckets))

	return &replicator{
		chrono:      config.Chrono,
		srcVbuckets: config.SrcVbuckets,
		dstVbuckets: config.DstVbuckets,
		latency:     config.Latency,
		maxSeqNos:   maxSeqNos,
	}, nil
}

func (r *replicator) checkVbucketsLocked() {
	if r.disabled {
		return
	}

	curTime := r.chrono.Now()
	var nextReplicateWake time.Time

	for vbIdx := range r.srcVbuckets {
		srcVbucket := r.srcVbuckets[vbIdx]
		dstVbucket := r.dstVbuckets[vbIdx]
		replicatedSeqNo := r.maxSeqNos[vbIdx]

		// Grab the maximum sequence number for this vbucket
		srcMaxSeqNo := srcVbucket.MaxSeqNo()

		// If we've already replicated it, we can immediately continue
		if replicatedSeqNo >= srcMaxSeqNo {
			continue
		}

		docs, err := srcVbucket.GetAllWithin(replicatedSeqNo, srcMaxSeqNo)
		if err != nil || len(docs) == 0 {
			// This would be extremely strange, but let's proceed.
			continue
		}

		for _, doc := range docs {
			replicationTime := doc.ModifiedTime.Add(r.latency)
			if curTime.Before(replicationTime) {
				if nextReplicateWake.IsZero() || replicationTime.Before(nextReplicateWake) {
					nextReplicateWake = replicationTime
				}
				break
			}

			dstVbucket.addRepDocMutation(doc)
		}
	}

	if !nextReplicateWake.IsZero() {
		replicateWait := nextReplicateWake.Sub(r.chrono.Now())
		r.chrono.AfterFunc(replicateWait, func() {
			r.lock.Lock()
			defer r.lock.Unlock()
			r.checkVbucketsLocked()
		})
	}
}

func (r *replicator) Pause() {
	r.lock.Lock()
	defer r.lock.Unlock()

	r.disabled = true
}

func (r *replicator) Resume() {
	r.lock.Lock()
	defer r.lock.Unlock()

	r.disabled = false

	r.checkVbucketsLocked()
}

func (r *replicator) Signal(vbIdx uint) {
	r.lock.Lock()
	defer r.lock.Unlock()

	r.checkVbucketsLocked()
}
