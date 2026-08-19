// Package readmodel owns the Config Server's immutable projection of Catalog
// metadata and configuration records. It deliberately duplicates the wire-
// compatible rules needed to validate MySQL rows so the Server never imports
// the Admin product's write model.
package readmodel
