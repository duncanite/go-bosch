// Package homeconnect is a BSH Home Connect appliance on the local network:
// a Bosch, Thermador, Siemens or Gaggenau dishwasher, oven, washer and the
// like. So far it knows an appliance's announcement: what one says of
// itself over DNS-SD, and how to find the appliances on a network through
// the means a caller lends.
//
// The standard library alone: no socket is opened here to search.
package homeconnect
