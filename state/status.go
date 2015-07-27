// Copyright 2014 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package state

import (
	"time"

	"github.com/juju/errors"
	jujutxn "github.com/juju/txn"
	"gopkg.in/mgo.v2"
	"gopkg.in/mgo.v2/bson"
	"gopkg.in/mgo.v2/txn"

	"github.com/juju/juju/leadership"
	"github.com/juju/juju/mongo"
)

// statusDoc represents a entity status in Mongodb.  The implicit
// _id field is explicitly set to the global key of the associated
// entity in the document's creation transaction, but omitted to allow
// direct use of the document in both create and update transactions.
type statusDoc struct {
	EnvUUID    string                 `bson:"env-uuid"`
	Status     Status                 `bson:"status"`
	StatusInfo string                 `bson:"statusinfo"`
	StatusData map[string]interface{} `bson:"statusdata"`

	// Updated might not be present on statuses dating from older versions
	// of juju. Do not dereference without checking.
	Updated *time.Time `bson:"updated"`

	// NeverSet is a short-term hack to work around a misfeature in service
	// status. To maintain current behaviour, we create service status docs
	// (and only service status documents) with NeverSet true; and then, when
	// reading them, if NeverSet is still true, we aggregate status from the
	// units instead.
	NeverSet bool `bson:"neverset"`
}

// mapKeys returns a copy of the supplied map, with all nested map[string]interface{}
// keys transformed by f. All other types are ignored.
func mapKeys(f func(string) string, input map[string]interface{}) map[string]interface{} {
	result := make(map[string]interface{})
	for key, value := range input {
		if submap, ok := value.(map[string]interface{}); ok {
			value = mapKeys(f, submap)
		}
		result[f(key)] = value
	}
	return result
}

// escapeKeys is used to escape bad keys in StatusData. A statusDoc without
// escaped keys is broken.
func escapeKeys(input map[string]interface{}) map[string]interface{} {
	return mapKeys(escapeReplacer.Replace, input)
}

// unescapeKeys is used to restore escaped keys from StatusData to their
// original values.
func unescapeKeys(input map[string]interface{}) map[string]interface{} {
	return mapKeys(unescapeReplacer.Replace, input)
}

// getStatus retrieves the status document associated with the given
// globalKey and converts it to a StatusInfo. If the status document
// is not found, a NotFoundError referencing badge will be returned.
func getStatus(st *State, globalKey, badge string) (_ StatusInfo, err error) {
	defer errors.DeferredAnnotatef(&err, "cannot get status")
	statuses, closer := st.getCollection(statusesC)
	defer closer()

	var doc statusDoc
	err = statuses.FindId(globalKey).One(&doc)
	if err == mgo.ErrNotFound {
		return StatusInfo{}, errors.NotFoundf(badge)
	} else if err != nil {
		return StatusInfo{}, errors.Trace(err)
	}

	return StatusInfo{
		Status:  doc.Status,
		Message: doc.StatusInfo,
		Data:    unescapeKeys(doc.StatusData),
		Since:   doc.Updated,
	}, nil
}

// setStatusParams configures a setStatus call. All parameters are presumed to
// be set to valid values unless otherwise noted.
type setStatusParams struct {

	// badge is used to specialize any NotFound error emitted.
	badge string

	// globalKey uniquely identifies the entity to which the status belongs.
	globalKey string

	// status is the status value.
	status Status

	// message is an optional string elaborating upon the status.
	message string

	// rawData is a map of arbitrary data elaborating upon the status and
	// message. Its keys are assumed not to have been escaped.
	rawData map[string]interface{}

	// token, if present, must accept an *[]txn.Op passed to its Check method,
	// and will prevent any change if it becomes invalid.
	token leadership.Token
}

// createStatusOpWithExcitingSideEffect returns the operation needed to create
// the given status document associated with the given globalKey...
//
// ...*and* tries to write a corresponding historical status document.
// This means that we may occasionally write spurious docs if people
// call this speculatively (so don't do that...) -- but also means that
// status-history is accurate from the very beginning of the entity's
// existence. We can't use mgo/txn consistency with statusHistory, so
// we err in favour of setting twice over never setting.
func createStatusOpWithExcitingSideEffect(st *State, globalKey string, doc statusDoc) txn.Op {
	probablyUpdateStatusHistory(st, globalKey, doc)
	return txn.Op{
		C:      statusesC,
		Id:     st.docID(globalKey),
		Assert: txn.DocMissing,
		Insert: doc,
	}
}

// removeStatusOp returns the operation needed to remove the status
// document associated with the given globalKey.
func removeStatusOp(st *State, globalKey string) txn.Op {
	return txn.Op{
		C:      statusesC,
		Id:     st.docID(globalKey),
		Remove: true,
	}
}

// setStatus inteprets the supplied params as documented on the type.
func setStatus(st *State, params setStatusParams) (err error) {
	defer errors.DeferredAnnotatef(&err, "cannot set status")

	// TODO(fwereade): this can/should probably be recording the time the
	// status was *set*, not the time it happened to arrive in state.
	// And we shouldn't be throwing away accuracy here -- neither to the
	// second right here *or* by serializing into mongo as a time.Time,
	// which also discards precision.
	// We should almost certainly be accepting StatusInfo in the exposed
	// SetStatus methods, for symetry with the Status methods.
	now := nowToTheSecond()
	doc := statusDoc{
		Status:     params.status,
		StatusInfo: params.message,
		StatusData: escapeKeys(params.rawData),
		Updated:    &now,
	}
	probablyUpdateStatusHistory(st, params.globalKey, doc)

	// Set the authoritative status document, or fail trying.
	buildTxn := updateStatusSource(st, params.globalKey, doc)
	if params.token != nil {
		buildTxn = wrapSource(buildTxn, params.token)
	}
	err = st.run(buildTxn)
	if cause := errors.Cause(err); cause == mgo.ErrNotFound {
		return errors.NotFoundf(params.badge)
	}
	return errors.Trace(err)
}

// updateStatusSource returns a transaction source that builds the operations
// necessary to set the supplied status (and to fail safely if leaked and
// executed late, so as not to overwrite more recent documents).
func updateStatusSource(st *State, globalKey string, doc statusDoc) jujutxn.TransactionSource {
	update := bson.D{{"$set", &doc}}
	return func(_ int) ([]txn.Op, error) {
		txnRevno, err := st.readTxnRevno(statusesC, globalKey)
		if err != nil {
			return nil, errors.Trace(err)
		}
		assert := bson.D{{"txn-revno", txnRevno}}
		return []txn.Op{{
			C:      statusesC,
			Id:     globalKey,
			Assert: assert,
			Update: update,
		}}, nil
	}
}

type historicalStatusDoc struct {
	Id         int                    `bson:"_id"`
	EnvUUID    string                 `bson:"env-uuid"`
	EntityId   string                 `bson:"entityid"`
	Status     Status                 `bson:"status"`
	StatusInfo string                 `bson:"statusinfo"`
	StatusData map[string]interface{} `bson:"statusdata"`

	// Updated might not be present on statuses copied by old versions of juju
	// from yet older versions of juju. Do not dereference without checking.
	Updated *time.Time `bson:"updated"`
}

func probablyUpdateStatusHistory(st *State, globalKey string, doc statusDoc) {
	// TODO(fwereade): we do NOT need every single status-history operation
	// to write to the same document in mongodb. If you need to order them,
	// use a time representation that does not discard precision, like an
	// int64 holding the time's UnixNanoseconds.
	id, err := st.sequence("statushistory")
	if err != nil {
		logger.Errorf("failed to generate id for status history: %v", err)
		return
	}
	historyDoc := &historicalStatusDoc{
		Id: id,
		// We can't guarantee that the statusDoc we're dealing with has the
		// env-uuid filled in; and envStateCollection does not trap inserts.
		// Good to be explicit; better to fix leaky abstraction.
		EnvUUID:    st.EnvironUUID(),
		Status:     doc.Status,
		StatusInfo: doc.StatusInfo,
		StatusData: doc.StatusData, // coming from a statusDoc, already escaped
		Updated:    doc.Updated,
		EntityId:   globalKey,
	}
	history, closer := st.getCollection(statusesHistoryC)
	defer closer()
	historyW := history.Writeable()
	if err := historyW.Insert(historyDoc); err != nil {
		logger.Errorf("failed to write status history: %v", err)
	}
}

func statusHistory(st *State, globalKey string, size int) ([]StatusInfo, error) {
	statusHistory, closer := st.getCollection(statusesHistoryC)
	defer closer()

	var docs []historicalStatusDoc
	query := statusHistory.Find(bson.D{{"entityid", globalKey}})
	err := query.Sort("-_id").Limit(size).All(&docs)
	if err == mgo.ErrNotFound {
		return []StatusInfo{}, errors.NotFoundf("status history")
	} else if err != nil {
		return []StatusInfo{}, errors.Annotatef(err, "cannot get status history")
	}

	results := make([]StatusInfo, len(docs))
	for i, doc := range docs {
		results[i] = StatusInfo{
			Status:  doc.Status,
			Message: doc.StatusInfo,
			Data:    unescapeKeys(doc.StatusData),
			Since:   doc.Updated,
		}
	}
	return results, nil
}

// PruneStatusHistory removes status history entries until
// only the maxLogsPerEntity newest records per unit remain.
func PruneStatusHistory(st *State, maxLogsPerEntity int) error {
	history, closer := st.getCollection(statusesHistoryC)
	defer closer()
	historyW := history.Writeable()

	// TODO(fwereade): This is a very strange implementation. Is it specced
	// that we should keep different spans of history for different entities?
	// It would seem normal to either keep entries for a fixed time (say 24h),
	// or to prune down to a target total data size by discarding the oldest
	// entries. This renders useless -- but is careful to keep -- every status
	// older than the oldest status of the most frequently updated entity...
	//
	// ...and it's really doing a *lot* of work to subtly corrupt the data.
	// If you want to break status history like this you can do it *much*
	// more efficiently.
	globalKeys, err := getEntitiesWithStatuses(historyW)
	if err != nil {
		return errors.Trace(err)
	}
	for _, globalKey := range globalKeys {
		keepUpTo, ok, err := getOldestTimeToKeep(historyW, globalKey, maxLogsPerEntity)
		if err != nil {
			return errors.Trace(err)
		}
		if !ok {
			continue
		}
		_, err = historyW.RemoveAll(bson.D{
			{"entityid", globalKey},
			{"_id", bson.M{"$lt": keepUpTo}},
		})
		if err != nil {
			return errors.Trace(err)
		}
	}
	return nil
}

// getOldestTimeToKeep returns the create time for the oldest
// status log to be kept.
func getOldestTimeToKeep(coll mongo.Collection, globalKey string, size int) (int, bool, error) {
	result := historicalStatusDoc{}
	err := coll.Find(bson.D{{"entityid", globalKey}}).Sort("-_id").Skip(size - 1).One(&result)
	if err == mgo.ErrNotFound {
		return -1, false, nil
	}
	if err != nil {
		return -1, false, errors.Trace(err)
	}
	return result.Id, true, nil

}

// getEntitiesWithStatuses returns the ids for all entities that
// have history entries
func getEntitiesWithStatuses(coll mongo.Collection) ([]string, error) {
	var entityKeys []string
	err := coll.Find(nil).Distinct("entityid", &entityKeys)
	if err != nil {
		return nil, errors.Trace(err)
	}
	return entityKeys, nil
}
