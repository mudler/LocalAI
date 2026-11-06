// SPDX-License-Identifier: MIT
#include "engine/models/sheetsage/session.h"
#include <algorithm>
#include <atomic>
#include <cmath>
#include <complex>
#include <cstring>
#include <iostream>
#include <mutex>
#include <condition_variable>
#include <thread>
#include <chrono>
#include <limits>
#ifdef _OPENMP
#include <omp.h>
#endif
using namespace engine::audio;
namespace ss = engine::models::sheetsage;
namespace engine::models::sheetsage {
std::vector<float> test_normalized_mel(const std::vector<float>&,const SheetSage2Assets&,size_t,int64_t&,FFTStop);
std::vector<float> test_legacy_normalized_mel(const std::vector<float>&,const SheetSage2Assets&,size_t,int64_t&);
std::vector<float> test_owned_normalized_mel(const std::vector<float>&,const SheetSage2Assets&,size_t,int64_t&,core::RunStopHandle);
}
namespace {
void require(bool b,const char*m){if(!b)throw std::runtime_error(m);}
struct Stop{std::atomic<bool> flag{false};}; Stop *current=nullptr,*late=nullptr;
engine::core::RunStopHandle owned;
bool requested(void*p)noexcept{return static_cast<Stop*>(p)->flag.load();}
std::atomic<int> target{-1};std::atomic<size_t> counts[12],workers{0},active{0},late_count{0};
std::atomic<bool> bad{false},gate{false},entered{false},release_gate{false},returned{false},expired{false};
std::mutex mutex;std::condition_variable cv;
std::atomic<size_t> residual[64];
std::atomic<bool> sparse_values{false};
size_t cell_contributions[64]{};
const std::complex<float>* actual_fft=nullptr;
size_t actual_fft_bins=0,actual_fft_frames=0,fft_cursor[64]{};
size_t oracle_samples=0,oracle_frames=0,oracle_bins=0;
std::vector<size_t> expected_reflections;
std::vector<float> expected_framed;
std::vector<std::complex<double>> expected_fft;
std::vector<float> expected_norm; double max_error=0;size_t fixtures=0;
void reset(Stop &s){current=&s;s.flag=false;owned.reset();late=nullptr;late_count=0;target=-1;workers=0;active=0;bad=false;gate=false;entered=false;release_gate=false;returned=false;expired=false;for(auto&c:counts)c=0;for(auto&r:residual)r=0;for(auto&v:cell_contributions)v=0;for(auto&v:fft_cursor)v=0;expected_norm.clear();}
void observe(int stage,size_t worker,size_t count,double value)noexcept{
 ++active;if(sparse_values&&stage<2&&worker<64){cell_contributions[worker]+=count;if(value!=double(cell_contributions[worker])*.0625)bad=true;}if(sparse_values&&stage==2&&worker<64){if(value!=double(cell_contributions[worker])*.0625)bad=true;cell_contributions[worker]=0;}

 if(stage<2&&current&&current->flag&&worker<64)residual[worker]+=count;
 if(!std::isfinite(value)||worker>=64)bad=true;counts[stage]+=count;if(worker<64)workers.fetch_or(size_t(1)<<worker);
 if(stage==5&&!expected_norm.empty()){size_t ix=counts[5]-1;if(ix>=expected_norm.size()||std::abs(value-expected_norm[ix])>6e-6+2e-6*std::abs(expected_norm[ix]))bad=true;}
 if(late){late->flag=true;++late_count;}
 if(target==stage){
  if(gate&&!entered.exchange(true)){cv.notify_all();std::unique_lock<std::mutex>l(mutex);if(!cv.wait_for(l,std::chrono::seconds(2),[]{return release_gate.load();})){expired=true;release_gate=true;}}
  else if(!gate){current->flag=true;if(owned)owned->cancel();}
 }
 --active;cv.notify_all();
}
void bits(const std::vector<float>&a,const std::vector<float>&b){require(a.size()==b.size()&&std::memcmp(a.data(),b.data(),a.size()*sizeof(float))==0,"legacy bitwise parity");}
std::vector<float> wave(int n,int count){std::vector<float>x;for(int j=0;j<count;++j)x.push_back(float((j*13+n)%31-15)/32);return x;}
ss::SheetSage2Assets assets(int n){ss::SheetSage2Assets a;a.config.n_fft=n;a.config.win_length=n-2;a.config.hop_length=3;a.config.mel_bins=3;for(int j=0;j<n-2;++j)a.stft_window.push_back(float(j%5+1)/8);AudioTensor d;d.shape={3,n/2+1};for(int m=0;m<3;++m)for(int f=0;f<n/2+1;++f)d.values.push_back(float((f+2*m)%7+1)/16);a.mel_filterbank=MelFilterbank().prepare_sparse(d);a.mel_mean={-.5f,.25f,.75f};a.mel_std={.5f,1.25f,2.f};return a;}
std::vector<float> direct(const std::vector<float>&x,const ss::SheetSage2Assets&a){
 expected_reflections.clear();oracle_samples=x.size();int n=int(a.config.n_fft),hop=int(a.config.hop_length),bins=n/2+1,frames=1+(int(x.size())+2*(n/2)-n)/hop,off=(n-int(a.stft_window.size()))/2;oracle_frames=frames;oracle_bins=bins;std::vector<float>mag(size_t(bins)*frames);expected_framed.assign(size_t(frames)*n,0);expected_fft.assign(size_t(frames)*bins,{});
 for(int f=0;f<bins;++f)for(int t=0;t<frames;++t){std::complex<double>s{};for(int j=0;j<n;++j){float v=0;if(j>=off&&j<off+int(a.stft_window.size())){int ix=t*hop-n/2+j,period=2*(int(x.size())-1);bool reflected=ix<0||ix>=int(x.size());ix=((ix%period)+period)%period;if(ix>=int(x.size()))ix=period-ix;if(reflected)expected_reflections.push_back(size_t(ix));v=x[ix]*a.stft_window[j-off];}expected_framed[size_t(t)*n+j]=v;double ang=-2*std::acos(-1.)*f*j/n;s+=double(v)*std::complex<double>(std::cos(ang),std::sin(ang));}expected_fft[size_t(t)*bins+f]=s;mag[size_t(f)*frames+t]=float(std::abs(s));}
 std::vector<float>out;for(int t=0;t<frames-1;++t)for(int m=0;m<a.config.mel_bins;++m){double sum=0;for(int f=a.mel_filterbank.starts[m];f<a.mel_filterbank.ends[m];++f){float v=mag[size_t(f)*frames+t];sum+=double(v*v)*double(a.mel_filterbank.dense.values[size_t(m)*bins+f]);}float power=std::max(float(sum),1e-10F),db=10.F*std::log10(power),sd=std::max(a.mel_std[m],1e-5F);out.push_back((db-a.mel_mean[m])/sd);}return out;
}
const unsigned frozen8[]={0xc1aa378e,0xc0fba69a,0xc0f9192e,0xc1a950dc,0xc0ea7005,0xc0a403a1,0xc1959b09,0xc0d8b086,0xc0d455ef,0xc1b5b6cc,0xc101dd8e,0xc0cb890e,0xc1acb370,0xc0fcb1ba,0xc0f3c92f,0xc1a4150b,0xc0e06570,0xc09b2734,0xc19778a4,0xc0daf1a8,0xc0d3332c};
const unsigned frozen15[]={0xc184c75e,0xc0aed48a,0xc04257b7,0xc1291131,0xc079c4a3,0xbffcbfca,0xc123525c,0xc0b65b5d,0xc0421632,0xc11362a8,0xc0bc1016,0xc05a22e0,0xc127af2c,0xc0be6608,0xc04008a6,0xc0d76a4d,0xc065b26d,0xc01437a9,0xc128d7fb,0xc0b80d1b,0xc04b41fa,0xc117667a,0xc0caebfb,0xc06a53dc,0xc12033a3,0xc0b358c8,0xc030251f,0xc0d8b23c,0xc0635d93,0xc014a6a4,0xc1290d2c,0xc09b97ca,0xc0449b89};
const unsigned frozen32[]={0x40418a73,0x3f966005,0x3f80c0e9,0x40c26021,0x3f81dcb5,0x3f71767a,0x40e21a15,0x3f480c1e,0x3f85becd,0x402f37d8,0x3f175306,0x3ee2fc1e,0x40a5a8dc,0x3d67c253,0x3f382976,0x40cf73ce,0x3f34a0b3,0x3f8fda5a,0x40bacf15,0x3f90be85,0x3f77f76c,0x4042d250,0xbeab0ebb,0x3ecf908c,0x40de6f80,0x3d0f331a,0x3f3fb41e,0x40f4b17a,0x3f90a00e,0x3f9b953c,0x41045659,0x3f52d1ce,0x3f8e0fdb,0x408e5c84,0xbf0db0fe,0x3e7048c8,0x40a789c7,0x3f4be8c3,0x3f72d16a,0x40d2377a,0x3f8731e5,0x3f9708cc,0x40bc9762,0x3e058e36,0x3f64a766,0x40924892,0xbf26ac8b,0x3e9683c2,0x40ae17ae,0x3f897287,0x3f8c2feb,0x40e91810,0x3f4d613b,0x3f8b8167,0x40ae41cc,0xbcfba2e0,0x3f325052,0x3f875180,0xbcc0bd8d,0x3ec74014,0x4059cd84,0x3eebfd80,0x3f6013b4,0x40932278,0x3fd53232,0x3f9f7606,0x4002430a,0x3ec08bd0,0x3f35b450};
struct AssetSnapshot {
 std::vector<float> dense,window,mean,stddev;std::vector<int64_t> starts,ends;
 explicit AssetSnapshot(const ss::SheetSage2Assets&a):dense(a.mel_filterbank.dense.values),window(a.stft_window),mean(a.mel_mean),stddev(a.mel_std),starts(a.mel_filterbank.starts),ends(a.mel_filterbank.ends){}
 void check(const ss::SheetSage2Assets&a)const{bits(dense,a.mel_filterbank.dense.values);bits(window,a.stft_window);bits(mean,a.mel_mean);bits(stddev,a.mel_std);require(starts==a.mel_filterbank.starts&&ends==a.mel_filterbank.ends,"immutable sparse metadata");}
};
void numerical(){
 Stop s;for(int n:{8,15,32}){reset(s);auto a=assets(n);auto x=wave(n,2*n+5);(void)direct(x,a);int64_t f=-77,g=-77;auto old=ss::test_legacy_normalized_mel(x,a,1,f);const unsigned* frozen=n==8?frozen8:(n==15?frozen15:frozen32);require(old.size()==size_t(n==8?21:(n==15?33:69)),"frozen helper length before memcmp");require(std::memcmp(old.data(),frozen,old.size()*sizeof(float))==0,"pre-edit actual helper frozen bits");for(bool cb:{false,true}){auto y=ss::test_normalized_mel(x,a,1,g,cb?FFTStop{requested,&s}:FFTStop{});bits(old,y);require(f==g,"helper frames parity");}auto ref=direct(x,a);require(old.size()==ref.size(),"DFT drop-final layout size");for(size_t j=0;j<old.size();++j){double e=std::abs(double(old[j])-ref[j]);max_error=std::max(max_error,e);require(e<=6e-6+2e-6*std::abs(ref[j]),"independent helper DFT normalization");}fixtures+=3;}
 // Float-square discriminating non-dyadic data, distinct batch/frequency/frame axes.
 for(int len:{1,3,4,5,65,133}){reset(s);int b=2,freq=139,stft=7,out=5;AudioTensor d;d.shape={6,freq};d.values.assign(6*freq,0);for(int m=0;m<6;++m)for(int k=0;k<len;++k)d.values[size_t(m)*freq+k+1]=float((k*7+m)%11+1)/16;auto bank=MelFilterbank().prepare_sparse(d);std::vector<float>mag(size_t(b)*freq*stft);for(size_t j=0;j<mag.size();++j)mag[j]=float(int(j*17%103)-51)/37.13f;auto copy=mag;auto old=MelFilterbank().compute_custom_sparse_from_magnitude(mag,b,freq,stft,out,bank);for(bool cb:{false,true}){auto r=MelFilterbank().compute_custom_sparse_from_magnitude(mag,b,freq,stft,out,bank,cb?FFTStop{requested,&s}:FFTStop{});require(r.status==FFTStatus::Completed&&r.output,"M11 sparse completion");bits(old.values,r.output->values);require(old.shape==r.output->shape,"sparse shape");}for(int bi=0;bi<b;++bi)for(int m=0;m<6;++m)for(int t=0;t<out;++t){double sum=0;for(int k=1;k<len+1;++k){float v=mag[(size_t(bi)*freq+k)*stft+t];sum+=double(v*v)*double(d.values[size_t(m)*freq+k]);}float v=float(sum);require(std::memcmp(&v,&old.values[(size_t(bi)*6+m)*out+t],4)==0,"ascending float-square oracle");}bits(copy,mag);fixtures+=3;}
}
void sparse_cancel(){Stop s;for(int team:{1,4}){
#ifdef _OPENMP
 omp_set_dynamic(0);omp_set_num_threads(team);
#endif
 for(int len:{1,3,65,133}){reset(s);int freq=139,frames=67;AudioTensor d;d.shape={65,freq};d.values.assign(65*freq,0);for(int m=0;m<65;++m)for(int k=0;k<len;++k)d.values[m*freq+k]=.25f;auto bank=MelFilterbank().prepare_sparse(d);std::vector<float>mag(size_t(freq)*frames,.5f);
 auto invoke=[&]{return MelFilterbank().compute_custom_sparse_from_magnitude(mag,1,freq,frames,frames,bank,{requested,&s});};
 sparse_values=true;auto control=invoke();require(control.output&&counts[2]==size_t(65*frames),"active control cells");require(counts[0]+counts[1]==size_t(65*frames*len),"contributions independent from cells");if(team==4)require(__builtin_popcountll(workers.load())>1,"actual OpenMP multiworker execution");
 for(int stage:{0,1,2}){if((stage==0&&len<4)||(stage==1&&len%4==0))continue;reset(s);target=stage;auto r=invoke();require(r.status==FFTStatus::Cancelled&&!r.output,"M7 Cancelled absent sparse output");require(counts[0]+counts[1]>0&&counts[0]+counts[1]<size_t(65*frames*len),"inner polling stops actual contributions");if(stage==0&&len==65)require(counts[1]==0,"four-wide cancellation polled before tail contribution");require(active==0&&!bad,"M10 sparse observer settlement");for(auto&r:residual)require(r<=64,"per-worker frequency residual <=64");++fixtures;}
 sparse_values=false;reset(s);s.flag=true;auto r=invoke();require(r.status==FFTStatus::Cancelled&&!r.output&&counts[0]+counts[1]+counts[2]==0,"M2 prestop no writes");++fixtures;
 }}
}
void helper_cancel(){Stop s;auto a=assets(32);auto x=wave(32,257);auto ref=direct(x,a);AssetSnapshot snapshot(a);int64_t frames=-77;
 for(int stage:{3,4,8,9,10,0,1,5}){reset(s);expected_norm=ref;target=stage;bool caught=false;try{(void)ss::test_normalized_mel(x,a,1,frames,{requested,&s});}catch(const engine::runtime::RunCancelled&){caught=true;}catch(const std::exception&){caught=false;}require(caught&&frames==-77,"M5/M12 authoritative child status typed cancellation sentinel");require(active==0&&!bad,"owner settled");require(counts[6]==0,"cancelled features never reach consumer boundary");if(stage==3||stage==4||stage>=8)require(counts[0]+counts[1]+counts[2]==0,"STFT cancel skips mel");if(stage==3)require(counts[3]<size_t(86*30),"forwarded STFT stop interrupts framing");if(stage==0||stage==1)require(counts[0]+counts[1]<size_t(3*85*17),"forwarded mel stop interrupts contributions");if(stage!=5)require(counts[5]==0,"child cancel skips normalization");if(stage==5)require(counts[5]>0&&counts[5]<=64&&counts[5]<ref.size(),"normalized 64-write bound");
 snapshot.check(a);reset(s);int64_t fresh=-77;auto b=ss::test_normalized_mel(x,a,1,fresh,{requested,&s});auto old=ss::test_legacy_normalized_mel(x,a,1,frames);bits(b,old);frames=-77;snapshot.check(a);fixtures+=2;}
 reset(s);owned=std::make_shared<engine::core::RunStopState>();owned->cancel();bool caught=false;try{ss::test_owned_normalized_mel(x,a,1,frames,owned);}catch(const engine::runtime::RunCancelled&){caught=true;}require(caught&&frames==-77,"actual owner handle typed cancellation");++fixtures;
 reset(s);owned=std::make_shared<engine::core::RunStopState>();auto actual_a=owned;target=0;caught=false;
 try{ss::test_owned_normalized_mel(x,a,1,frames,actual_a);}catch(const engine::runtime::RunCancelled&){caught=true;}
 require(caught&&actual_a->cancelled()&&frames==-77,"actual active owner bridge");reset(s);auto actual_b=std::make_shared<engine::core::RunStopState>();auto fresh_owned=ss::test_owned_normalized_mel(x,a,1,frames,actual_b);auto legacy_owned=ss::test_legacy_normalized_mel(x,a,1,frames);bits(fresh_owned,legacy_owned);require(!actual_b->cancelled(),"actual owned B fresh state");fixtures+=2;
 // Same actual A callback state: A completes, then its late transition occurs in B work.
 Stop astate,bstate;reset(astate);int64_t af=-77;auto ay=ss::test_normalized_mel(x,a,1,af,{requested,&astate});require(!astate.flag,"A initially completed clear");
 reset(bstate);gate=true;target=5;int64_t bf=-77;std::vector<float>by;
 std::thread bworker([&]{by=ss::test_normalized_mel(x,a,1,bf,{requested,&bstate});returned=true;cv.notify_all();});
 bool reached=false;{std::unique_lock<std::mutex>l(mutex);reached=cv.wait_for(l,std::chrono::seconds(2),[]{return entered.load()||returned.load();})&&entered;
 // This is exactly A's state used above, not an unrelated handle.
 if(reached){astate.flag=true;++late_count;}release_gate=true;cv.notify_all();}bworker.join();
 require(reached&&astate.flag&&late_count==1&&!bstate.flag&&!expired,"M14 actual retained A transition while B gated");bits(ay,by);snapshot.check(a);++fixtures;
}
void validation_and_publication(){
 Stop s;auto a=assets(8);auto x=wave(8,21);(void)direct(x,a);int64_t frames=-77;reset(s);s.flag=true;
 auto invalid=a;invalid.mel_filterbank.starts[0]=5;invalid.mel_filterbank.ends[0]=0;
 bool failed=false;try{ss::test_normalized_mel(x,invalid,1,frames,{requested,&s});}catch(const engine::runtime::RunCancelled&){throw std::runtime_error("M1 validation relabelled as cancellation");}catch(const std::runtime_error&e){failed=std::string(e.what())=="SparseMelFilterbank metadata range mismatch";}
 require(failed&&frames==-77,"M1 malformed helper rejects before prestop");++fixtures;
 AudioTensor zero;zero.shape={1,5};zero.values.assign(5,0);auto bank=MelFilterbank().prepare_sparse(zero);std::vector<float>mag(15,.5f);failed=false;
 try{MelFilterbank().compute_custom_sparse_from_magnitude(mag,1,5,3,2,bank,{requested,&s});}catch(const std::runtime_error&e){failed=std::string(e.what())=="SparseMelFilterbank metadata range mismatch";}
 require(failed,"all-zero prepared row rejection preserved");++fixtures;
 for(int mode=0;mode<5;++mode){auto b=a.mel_filterbank;auto v=mag;int out=2;
 if(mode==0)out=4;if(mode==1)v.pop_back();if(mode==2)b.dense.shape={3};if(mode==3)b.starts.pop_back();if(mode==4)b.ends[0]=6;
 failed=false;try{MelFilterbank().compute_custom_sparse_from_magnitude(v,1,5,3,out,b,{requested,&s});}catch(const std::runtime_error&){failed=true;}require(failed,"M1 sparse validation before stop");++fixtures;}
 for(bool stopped:{false,true})for(int which=0;which<3;++which){reset(s);s.flag=stopped;auto b=a.mel_filterbank;int64_t batch=1,freq=5,stft=3;
 if(which==0)batch=std::numeric_limits<int64_t>::max();if(which==1)freq=std::numeric_limits<int64_t>::max();if(which==2)b.dense.shape[0]=std::numeric_limits<int64_t>::max();
 bool ordinary=false;try{MelFilterbank().compute_custom_sparse_from_magnitude(mag,batch,freq,stft,2,b,{requested,&s});}catch(const engine::runtime::RunCancelled&){ordinary=false;}catch(const std::exception&){ordinary=true;}require(ordinary,"overflow dimensions ordinary validation with tiny buffers");++fixtures;}
 // Each real normalized store arms a request only on the final write.
 reset(s);target=7;owned=std::make_shared<engine::core::RunStopState>();auto final_handle=owned;auto baseline=direct(x,a);expected_norm=baseline;
 bool caught=false;try{ss::test_owned_normalized_mel(x,a,1,frames,final_handle);}catch(const engine::runtime::RunCancelled&){caught=true;}
 require(caught&&frames==-77&&counts[6]==0&&counts[5]==baseline.size(),"M11 final-write cancellation prevents publication");++fixtures;
 reset(s);auto success_handle=std::make_shared<engine::core::RunStopState>();auto published=ss::test_owned_normalized_mel(x,a,1,frames,success_handle);auto legacy_published=ss::test_legacy_normalized_mel(x,a,1,frames);bits(published,legacy_published);require(counts[6]==1,"owned successful consumer exactly once");++fixtures;
 reset(s);a.mel_std={0,1e-6f,2};a.mel_mean={-.5f,.25f,.75f};x.assign(x.size(),0);auto expected=direct(x,a);auto actual=ss::test_normalized_mel(x,a,1,frames,{requested,&s});bits(actual,expected);++fixtures;
}

void multiworker_settlement(){
 for(int stage:{0,1,2}){
 Stop s;reset(s);gate=true;target=stage;AudioTensor dense;dense.shape={65,139};dense.values.assign(65*139,0);for(int m=0;m<65;++m)for(int f=0;f<65;++f)dense.values[m*139+f]=.25f;auto bank=MelFilterbank().prepare_sparse(dense);const auto bankcopy=bank;std::vector<float>mag(139*67,.5f),magcopy=mag;STFTResult outcome{FFTStatus::Completed,std::nullopt};
 std::thread owner([&]{
#ifdef _OPENMP
 omp_set_dynamic(0);omp_set_num_threads(4);
#endif
 outcome=MelFilterbank().compute_custom_sparse_from_magnitude(mag,1,139,67,67,bank,{requested,&s});returned=true;cv.notify_all();});
 bool reached=false,early=false;{std::unique_lock<std::mutex>l(mutex);reached=cv.wait_for(l,std::chrono::seconds(2),[]{return entered.load()||returned.load();})&&entered;early=returned;s.flag=true;release_gate=true;cv.notify_all();}owner.join();
 require(reached&&!early&&!expired&&active==0,"real >=4096 mel multiworker external gate settles");require(__builtin_popcountll(workers.load())>1,"gated actual multiworker team");require(outcome.status==FFTStatus::Cancelled&&!outcome.output,"multiworker gated absent output");for(auto&r:residual)require(r<=64,"external stop per-worker <=64 frequency contributions");bits(mag,magcopy);bits(bank.dense.values,bankcopy.dense.values);require(bank.starts==bankcopy.starts&&bank.ends==bankcopy.ends,"cancel metadata immutable");
 reset(s);auto fresh=MelFilterbank().compute_custom_sparse_from_magnitude(mag,1,139,67,67,bank,{requested,&s});require(fresh.output.has_value(),"post gate fresh B");for(float v:fresh.output->values)require(v==65*.0625f,"full B exact independent sparse parity");++fixtures;
 }
}
void settlement(){Stop s;auto a=assets(32);auto x=wave(32,257);auto ref=direct(x,a);for(int stage:{3,4,8,9,10,0,1,2,5}){reset(s);gate=true;target=stage;int64_t frames=-77;std::atomic<bool>caught{false};std::thread owner([&]{try{ss::test_normalized_mel(x,a,1,frames,{requested,&s});}catch(const engine::runtime::RunCancelled&){caught=true;}returned=true;cv.notify_all();});bool reached=false,early=false;{std::unique_lock<std::mutex>l(mutex);reached=cv.wait_for(l,std::chrono::seconds(2),[]{return entered.load()||returned.load();})&&entered;early=returned;s.flag=true;release_gate=true;cv.notify_all();}owner.join();require(reached&&!early&&!expired,"M10 owner held while actual worker gated");require(caught&&active==0&&frames==-77,"external stop settles before owner translation");++fixtures;}}
}
namespace engine::audio::mel_testing{void worked(int stage,size_t worker,size_t count,double value)noexcept{observe(stage,worker,count,value);}}
namespace engine::audio::stft_testing{
void stored(int stage,size_t worker,size_t begin,size_t count,float value)noexcept{
 if(stage==0){size_t index=begin+count-1;if(!expected_framed.empty()&&(index>=expected_framed.size()||value!=expected_framed[index]))bad=true;observe(3,worker,count,value);}
 if(stage==1){if(value<0||value>=oracle_samples||std::floor(value)!=value||std::find(expected_reflections.begin(),expected_reflections.end(),size_t(value))==expected_reflections.end())bad=true;observe(8,worker,count,value);}
 if(stage==2||stage==3){size_t flat=stage==2?begin/2:begin;size_t frames=oracle_frames,bins=oracle_bins;
 if(frames&&bins){size_t f=flat/frames,t=flat%frames;size_t ix=t*bins+f;if(ix<expected_fft.size()){auto z=expected_fft[ix];double expected=stage==2?z.real():std::abs(z);if(std::abs(value-expected)>6e-6+2e-6*std::abs(expected))bad=true;}else bad=true;}
 observe(stage==2?9:10,worker,count,value);}
}
bool fft_returned(FFTStatus)noexcept{return false;}
void fft_context(const std::complex<float>*p,size_t frames,size_t bins)noexcept{actual_fft=p;actual_fft_frames=frames;actual_fft_bins=bins;}
}
namespace engine::audio::fft_testing{
void plan_miss(size_t)noexcept{}
void plan_published(size_t)noexcept{}
void group_stored(size_t worker,size_t count,bool)noexcept{observe(4,worker,count,1);}
}
namespace engine::models::sheetsage::mel_testing{
void normalized(size_t index,float value)noexcept{observe(5,0,1,value);if(target==7&&index+1==expected_norm.size()){current->flag=true;if(owned)owned->cancel();}}
void child_returned(int,FFTStatus status)noexcept{if(status==FFTStatus::Cancelled)current->flag=false;}
void consuming()noexcept{observe(6,0,1,1);}
}
int main(){try{size_t before=fixtures;numerical();std::cout<<"fixtures numerical="<<fixtures-before<<" (3 helper keys x3 controls; 6 sparse supports x3 controls)\n";before=fixtures;sparse_cancel();std::cout<<"fixtures sparse_cancel="<<fixtures-before<<" (2 teams x [2+2+3+3 active paths +4 prestops])\n";before=fixtures;helper_cancel();std::cout<<"fixtures helper_cancel="<<fixtures-before<<" (8 cancel/recovery pairs +prestop +owned cancel/recovery +late actual A)\n";before=fixtures;validation_and_publication();std::cout<<"fixtures validation_publication="<<fixtures-before<<" (7 validation +6 overflow +final-write +owned success +clamps)\n";before=fixtures;settlement();std::cout<<"fixtures settlement="<<fixtures-before<<" (9 framing/reflection/FFT/transpose/magnitude/mel-four/mel-tail/cell/norm external gates)\n";before=fixtures;multiworker_settlement();std::cout<<"fixtures multiworker_settlement="<<fixtures-before<<" (four/tail/cell gates + full fresh B checks)\n";std::cout<<"PASS fixtures="<<fixtures<<" max_normalized_DFT_error="<<max_error<<"\n";return 0;}catch(const std::exception&e){std::cerr<<"FAIL: "<<e.what()<<"\n";return 1;}}
namespace audio_cpp_fft_internal::testing {
void submitting(size_t){}
void worker(){}
void stored(bool,size_t count,size_t worker,size_t team)noexcept{
 double value=0;
 if(actual_fft&&worker<64&&team){size_t base=actual_fft_frames/team*worker+std::min(worker,actual_fft_frames%team);size_t frame=base+fft_cursor[worker];fft_cursor[worker]+=count;if(frame<actual_fft_frames){value=actual_fft[frame*actual_fft_bins].real();for(size_t t=frame;t<frame+count&&t<actual_fft_frames;++t)for(size_t k=0;k<actual_fft_bins;++k){size_t ix=t*actual_fft_bins+k;if(ix>=expected_fft.size()){bad=true;continue;}auto got=actual_fft[ix];auto expected=expected_fft[ix];if(std::abs(got.real()-expected.real())>2e-6+2e-6*std::abs(expected.real())||std::abs(got.imag()-expected.imag())>2e-6+2e-6*std::abs(expected.imag()))bad=true;}}else bad=true;}
 observe(4,worker,count,value);
}
void plan_miss(size_t,bool)noexcept{}
}
