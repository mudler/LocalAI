// SPDX-License-Identifier: MIT
#pragma once
#include "chat.h"

// Objects are unordered JSON mappings; arrays retain their order and scalar
// equality retains the JSON library's numeric semantics.
inline bool semantic_equals(const common_json &actual, const common_json &expected) {
 if(actual.is_object() && expected.is_object()) {
  if(actual.size()!=expected.size())return false;
  for(auto it=expected.begin();it!=expected.end();++it)
   if(!actual.contains(it.key()) || !semantic_equals(actual.at(it.key()),it.value()))return false;
  return true;
 }
 if(actual.is_array() && expected.is_array()) {
  if(actual.size()!=expected.size())return false;
  for(size_t i=0;i<actual.size();++i)if(!semantic_equals(actual[i],expected[i]))return false;
  return true;
 }
 return actual==expected;
}
