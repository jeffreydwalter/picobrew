/* Include the actual radio and broker objects in the off-device SRAM budget.
 * HTTP reuses the compact application's existing single-client context. */
#include "cc3000_broker.h"

struct zym_cc3000 candidate_radio;
struct zym_cc3000_broker candidate_broker;
